package routing

import (
	"context"
	"errors"
	"fmt"
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

// --- FallbackUsed/FallbackFrom reporting correctness (from the
// context-window-escalation live-wiring fix) ---

type stubExecProvider struct {
	name string
	resp *schema.ResponseEnvelope
	err  error
}

func (s *stubExecProvider) Name() string { return s.name }
func (s *stubExecProvider) Chat(_ context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	return s.resp, s.err
}
func (s *stubExecProvider) Stream(_ context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
	return s.err
}

func fastRetryCfgSingleAttempt() RetryConfig {
	return RetryConfig{MaxAttempts: 1, InitialBackoff: 0, MaxBackoff: 0, BackoffFactor: 1}
}

func TestExecute_NoFallbackNeeded(t *testing.T) {
	lookup := func(model string) (provider.Provider, error) {
		return &stubExecProvider{name: "p", resp: &schema.ResponseEnvelope{ID: "ok"}}, nil
	}
	decision := RoutingDecision{Model: "gpt-5.3-codex"}
	result, err := Execute(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfgSingleAttempt())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FallbackUsed {
		t.Error("expected FallbackUsed=false when the primary succeeds")
	}
	if result.Model != "gpt-5.3-codex" {
		t.Errorf("Model: got %q", result.Model)
	}
}

func TestExecute_ContextLengthEscalation_ReportsFallbackUsed(t *testing.T) {
	// Regression: escalation resets the loop index to rebuild the candidate
	// list, which used to make FallbackUsed derive from the post-reset index
	// (always 0 on the escalated attempt) and so always report false, even
	// though the response came from a completely different model.
	lookup := func(model string) (provider.Provider, error) {
		if model == "gpt-5.3-codex" {
			return &stubExecProvider{name: "primary", err: fmt.Errorf("openai: status 400: context_length_exceeded")}, nil
		}
		return &stubExecProvider{name: "fallback", resp: &schema.ResponseEnvelope{ID: "escalated-ok"}}, nil
	}
	decision := RoutingDecision{
		Model:         "gpt-5.3-codex",                      // 128k window
		FallbackChain: []Candidate{{Model: "llama-4-scout"}}, // 10M window
	}
	result, err := Execute(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfgSingleAttempt())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "llama-4-scout" {
		t.Fatalf("Model: got %q, want llama-4-scout", result.Model)
	}
	if !result.FallbackUsed {
		t.Error("expected FallbackUsed=true after context-length escalation")
	}
	if result.FallbackFrom != "gpt-5.3-codex" {
		t.Errorf("FallbackFrom: got %q, want gpt-5.3-codex", result.FallbackFrom)
	}
}

func TestExecute_NormalFallbackReportsFallbackUsed(t *testing.T) {
	lookup := func(model string) (provider.Provider, error) {
		if model == "gpt-4o" {
			return &stubExecProvider{name: "primary", err: fmt.Errorf("openai: status 500: internal error")}, nil
		}
		return &stubExecProvider{name: "fallback", resp: &schema.ResponseEnvelope{ID: "ok"}}, nil
	}
	decision := RoutingDecision{Model: "gpt-4o", FallbackChain: []Candidate{{Model: "gpt-4o-mini"}}}
	result, err := Execute(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfgSingleAttempt())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FallbackUsed {
		t.Error("expected FallbackUsed=true when falling through to the next candidate")
	}
}

func TestExecute_ExhaustedChainAttributesFailureToLastCandidate(t *testing.T) {
	// Regression: when every candidate fails, the returned ExecuteResult used
	// to have a zero-value Model/Provider, so a caller logging the failure
	// had nothing to fall back on except the ORIGINAL primary model/provider
	// — misattributing an error from a later fallback candidate (e.g.
	// "deepseek provider not configured") to the primary's provider
	// ("llama-4-scout" / groq) that never actually produced it.
	lookup := func(model string) (provider.Provider, error) {
		if model == "llama-4-scout" {
			return &stubExecProvider{name: "llama", err: fmt.Errorf("groq: status 404: model not found")}, nil
		}
		// The second candidate's provider isn't configured at all.
		return nil, fmt.Errorf("deepseek provider not configured")
	}
	decision := RoutingDecision{
		Model:         "llama-4-scout",
		FallbackChain: []Candidate{{Model: "deepseek-v3-0324"}},
	}
	result, err := Execute(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfgSingleAttempt())
	if err == nil {
		t.Fatal("expected an error when every candidate fails")
	}
	if err.Error() != "deepseek provider not configured" {
		t.Fatalf("expected the last candidate's error to win, got %q", err.Error())
	}
	if result.Model != "deepseek-v3-0324" {
		t.Errorf("Model: got %q, want deepseek-v3-0324 (the candidate that actually produced the final error)", result.Model)
	}
	// The failing candidate's own lookup never returned a provider instance,
	// so Provider reflects the last one that *did* resolve (llama) rather
	// than being left nil — still strictly more informative than the
	// zero-value ExecuteResult callers got before this fix.
	if result.Provider == nil || result.Provider.Name() != "llama" {
		t.Errorf("Provider: got %v, want the last successfully-resolved provider (llama)", result.Provider)
	}
}

func TestExecuteStream_ContextLengthEscalation_ReportsFallbackUsed(t *testing.T) {
	lookup := func(model string) (provider.Provider, error) {
		if model == "gpt-5.3-codex" {
			return &stubExecProvider{name: "primary", err: fmt.Errorf("openai: status 400: context_length_exceeded")}, nil
		}
		return &stubExecProvider{name: "fallback", err: nil}, nil
	}
	decision := RoutingDecision{
		Model:         "gpt-5.3-codex",
		FallbackChain: []Candidate{{Model: "llama-4-scout"}},
	}
	result, err := ExecuteStream(context.Background(), decision, &schema.RequestEnvelope{}, lookup, fastRetryCfgSingleAttempt(), func(provider.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "llama-4-scout" || !result.FallbackUsed {
		t.Errorf("expected escalated fallback, got Model=%q FallbackUsed=%v", result.Model, result.FallbackUsed)
	}
}
