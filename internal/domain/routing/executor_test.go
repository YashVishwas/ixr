package routing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// stubProvider is a minimal provider.Provider for executor tests.
type stubProvider struct {
	name string

	chatCalls int
	chatFn    func(callN int) (*schema.ResponseEnvelope, error)

	streamCalls int
	streamFn    func(callN int, fn func(provider.StreamChunk) error) error
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Chat(_ context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	s.chatCalls++
	return s.chatFn(s.chatCalls)
}

func (s *stubProvider) Stream(_ context.Context, _ *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	s.streamCalls++
	return s.streamFn(s.streamCalls, fn)
}

var fastRetryCfg = RetryConfig{
	MaxAttempts:    3,
	InitialBackoff: time.Millisecond,
	MaxBackoff:     5 * time.Millisecond,
	BackoffFactor:  2.0,
}

func TestChatWithRetry_SucceedsAfterTransientFailures(t *testing.T) {
	p := &stubProvider{name: "test", chatFn: func(callN int) (*schema.ResponseEnvelope, error) {
		if callN < 3 {
			return nil, errors.New("test: connection refused")
		}
		return &schema.ResponseEnvelope{ID: "ok"}, nil
	}}

	resp, attempts, err := chatWithRetry(context.Background(), p, &schema.RequestEnvelope{}, fastRetryCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts: got %d, want 3", attempts)
	}
	if resp.ID != "ok" {
		t.Errorf("response: got %+v", resp)
	}
}

func TestChatWithRetry_SkipsRetryOn4xx(t *testing.T) {
	p := &stubProvider{name: "test", chatFn: func(callN int) (*schema.ResponseEnvelope, error) {
		return nil, errors.New("test: status 401: unauthorized")
	}}

	_, attempts, err := chatWithRetry(context.Background(), p, &schema.RequestEnvelope{}, fastRetryCfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d, want 1 (4xx must not retry)", attempts)
	}
}

func TestChatWithRetry_ExhaustsAttemptsOnPersistentFailure(t *testing.T) {
	p := &stubProvider{name: "test", chatFn: func(callN int) (*schema.ResponseEnvelope, error) {
		return nil, errors.New("test: connection refused")
	}}

	_, attempts, err := chatWithRetry(context.Background(), p, &schema.RequestEnvelope{}, fastRetryCfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != fastRetryCfg.MaxAttempts {
		t.Errorf("attempts: got %d, want %d", attempts, fastRetryCfg.MaxAttempts)
	}
}

func TestExecute_FallsBackAfterPrimaryExhaustsRetries(t *testing.T) {
	primary := &stubProvider{name: "primary", chatFn: func(int) (*schema.ResponseEnvelope, error) {
		return nil, errors.New("primary: connection refused")
	}}
	fallback := &stubProvider{name: "fallback", chatFn: func(int) (*schema.ResponseEnvelope, error) {
		return &schema.ResponseEnvelope{ID: "fallback-ok"}, nil
	}}

	lookup := func(model string) (provider.Provider, error) {
		if model == "model-a" {
			return primary, nil
		}
		return fallback, nil
	}

	decision := RoutingDecision{
		Model:         "model-a",
		FallbackChain: []Candidate{{Model: "model-b"}},
	}

	result, err := Execute(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FallbackUsed || result.FallbackFrom != "model-a" || result.Model != "model-b" {
		t.Errorf("expected fallback to model-b, got %+v", result)
	}
	if primary.chatCalls != fastRetryCfg.MaxAttempts {
		t.Errorf("primary chat calls: got %d, want %d (should exhaust retries before falling back)", primary.chatCalls, fastRetryCfg.MaxAttempts)
	}
	if fallback.chatCalls != 1 {
		t.Errorf("fallback chat calls: got %d, want 1", fallback.chatCalls)
	}
}

func TestStreamWithRetry_RetriesBeforeAnyChunkWritten(t *testing.T) {
	p := &stubProvider{streamFn: func(callN int, fn func(provider.StreamChunk) error) error {
		if callN < 2 {
			return errors.New("test: connection refused")
		}
		return fn(provider.StreamChunk{Delta: schema.Message{Content: "hi"}})
	}}

	var got []string
	attempts, err := streamWithRetry(context.Background(), p, &schema.RequestEnvelope{}, fastRetryCfg, func(c provider.StreamChunk) error {
		got = append(got, c.Delta.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts: got %d, want 2", attempts)
	}
	if len(got) != 1 {
		t.Errorf("chunks delivered to caller: got %d, want 1 (no duplication)", len(got))
	}
}

func TestStreamWithRetry_DoesNotRetryAfterPartialWrite(t *testing.T) {
	p := &stubProvider{streamFn: func(callN int, fn func(provider.StreamChunk) error) error {
		// Emits one real chunk, then the connection drops mid-stream.
		if err := fn(provider.StreamChunk{Delta: schema.Message{Content: "partial"}}); err != nil {
			return err
		}
		return errors.New("test: connection reset by peer")
	}}

	var got []string
	attempts, err := streamWithRetry(context.Background(), p, &schema.RequestEnvelope{}, fastRetryCfg, func(c provider.StreamChunk) error {
		got = append(got, c.Delta.Content)
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d, want 1 (must not retry once a chunk was already sent to the caller)", attempts)
	}
	if len(got) != 1 {
		t.Errorf("chunks delivered to caller: got %d, want exactly 1 — a retry here would duplicate output on the wire", len(got))
	}
}
