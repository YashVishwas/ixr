package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// stubProvider is a minimal provider.Provider for testing.
type stubProvider struct {
	name string
	resp *schema.ResponseEnvelope
	err  error
	chat func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error)
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	if s.chat != nil {
		return s.chat(ctx, req)
	}
	return s.resp, s.err
}
func (s *stubProvider) Stream(_ context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
	return s.err
}

func fixedRouter(p provider.Provider) Router {
	return func(_ string) (provider.Provider, error) { return p, nil }
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestChatHandler_MethodNotAllowed(t *testing.T) {
	h := NewChatHandler(fixedRouter(&stubProvider{}), nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", w.Code)
	}
}

func TestChatHandler_BadJSON(t *testing.T) {
	h := NewChatHandler(fixedRouter(&stubProvider{}), nil)
	w := post(h, "not json")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestChatHandler_MissingModel(t *testing.T) {
	h := NewChatHandler(fixedRouter(&stubProvider{}), nil)
	w := post(h, `{"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestChatHandler_StreamReturnsSSE(t *testing.T) {
	h := NewChatHandler(fixedRouter(&stubProvider{name: "test"}), nil)
	w := post(h, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type: got %q, want text/event-stream", ct)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("body should contain [DONE], got %q", w.Body.String())
	}
}

func TestChatHandler_RouterError(t *testing.T) {
	router := Router(func(_ string) (provider.Provider, error) {
		return nil, fmt.Errorf("unknown model")
	})
	h := NewChatHandler(router, nil)
	w := post(h, `{"model":"unknown-model","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestChatHandler_ProviderError(t *testing.T) {
	p := &stubProvider{name: "test", err: fmt.Errorf("upstream down")}
	h := NewChatHandler(fixedRouter(p), nil)
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", w.Code)
	}
}

// TestChatHandler_RetriesTransientProviderErrors locks in the fix for a bug
// where routing.Execute (retry + fallback + circuit breaker) was fully
// built and tested but never called from ServeHTTP — every request bypassed
// it entirely via a single bare p.Chat() call, so a transient failure never
// got a second attempt.
func TestChatHandler_RetriesTransientProviderErrors(t *testing.T) {
	calls := 0
	p := &stubProvider{name: "test", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		if calls < 3 {
			return nil, fmt.Errorf("connection refused")
		}
		return &schema.ResponseEnvelope{ID: "ok", Choices: []schema.Choice{{}}}, nil
	}}
	h := NewChatHandler(fixedRouter(p), nil, WithRetryConfig(fastRetryConfig))
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if calls != 3 {
		t.Errorf("provider calls: got %d, want 3 (2 failures + 1 success)", calls)
	}
}

// TestChatHandler_SkipsRetryOn4xx confirms a client error still fails fast
// (one attempt) rather than burning the retry budget on a request that will
// never succeed.
func TestChatHandler_SkipsRetryOn4xx(t *testing.T) {
	calls := 0
	p := &stubProvider{name: "test", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		return nil, fmt.Errorf("test: status 401: unauthorized")
	}}
	h := NewChatHandler(fixedRouter(p), nil, WithRetryConfig(fastRetryConfig))
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", w.Code)
	}
	if calls != 1 {
		t.Errorf("provider calls: got %d, want 1 (4xx must not retry)", calls)
	}
}

// TestChatHandler_CircuitBreakerBlocksDirectModelRequest confirms an open
// breaker short-circuits a direct "model": request before it ever reaches
// the provider — previously the breaker was only consulted for model:"auto"
// candidate filtering, never for a request naming a model directly.
func TestChatHandler_CircuitBreakerBlocksDirectModelRequest(t *testing.T) {
	cb := circuitbreaker.NewRegistry(circuitbreaker.Policy{
		SuccessRateThreshold: 0.90,
		WindowDuration:       time.Minute,
		MinRequests:          1,
		HalfOpenAfter:        time.Hour,
		ProbeCount:           1,
	})
	cb.RecordOutcome("gpt-4o", false) // one failure trips a MinRequests:1 breaker

	calls := 0
	p := &stubProvider{name: "test", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		return &schema.ResponseEnvelope{ID: "ok", Choices: []schema.Choice{{}}}, nil
	}}
	h := NewChatHandler(fixedRouter(p), nil, WithCBRegistry(cb))
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Errorf("provider calls: got %d, want 0 (breaker should short-circuit before calling the provider)", calls)
	}
}

var fastRetryConfig = routing.RetryConfig{
	MaxAttempts:    3,
	InitialBackoff: time.Millisecond,
	MaxBackoff:     5 * time.Millisecond,
	BackoffFactor:  2.0,
}

func TestChatHandler_HappyPath(t *testing.T) {
	p := &stubProvider{
		name: "test",
		resp: &schema.ResponseEnvelope{
			ID:    "resp-1",
			Model: "gpt-4o",
			Choices: []schema.Choice{
				{Index: 0, Message: schema.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
			},
		},
	}
	h := NewChatHandler(fixedRouter(p), nil)
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "resp-1" {
		t.Errorf("ID: got %q, want resp-1", resp.ID)
	}
}

func TestChatHandler_ModelAutoResolvesCatalog(t *testing.T) {
	var gotModel string
	router := Router(func(model string) (provider.Provider, error) {
		gotModel = model
		return &stubProvider{
			name: "openai",
			resp: &schema.ResponseEnvelope{
				ID:      "r-auto",
				Model:   model,
				Choices: []schema.Choice{{}},
			},
		}, nil
	})
	h := NewChatHandler(router, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IXR-Task", "coding")
	req.Header.Set("X-IXR-Latency", "sensitive")
	req.Header.Set("X-IXR-Budget", "50")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if gotModel != "gpt-5.3-codex" {
		t.Fatalf("prefix router model: got %q, want gpt-5.3-codex", gotModel)
	}
}

func TestChatHandler_ModelAutoNoMatch(t *testing.T) {
	h := NewChatHandler(fixedRouter(&stubProvider{}), nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IXR-Budget", "0.0001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestChatHandler_UseCaseHeader(t *testing.T) {
	published := make(chan *schema.CallEvent, 1)
	fakeBus := &captureBus{ch: published}

	p := &stubProvider{
		name: "test",
		resp: &schema.ResponseEnvelope{ID: "r1", Choices: []schema.Choice{{}}},
	}
	h := NewChatHandler(fixedRouter(p), fakeBus)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IXR-UseCase", "test-case-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ev := <-published
	if ev.UseCaseID != "test-case-42" {
		t.Errorf("use_case_id: got %q, want test-case-42", ev.UseCaseID)
	}
}

func TestChatHandler_ComputesCostAndPropagatesIdentity(t *testing.T) {
	published := make(chan *schema.CallEvent, 1)
	fakeBus := &captureBus{ch: published}

	p := &stubProvider{
		name: "anthropic",
		resp: &schema.ResponseEnvelope{
			ID:      "r1",
			Model:   "claude-sonnet-4-6",
			Choices: []schema.Choice{{}},
			Usage:   schema.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000},
		},
	}
	h := NewChatHandler(fixedRouter(p), fakeBus)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	id := schema.Identity{TenantID: "acme", TeamID: "eng", UserID: "alice"}
	req = req.WithContext(identity.WithIdentity(req.Context(), id))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	ev := readEvent(t, published)
	if ev.TenantID != "acme" || ev.TeamID != "eng" || ev.UserID != "alice" {
		t.Fatalf("identity not propagated: got tenant=%q team=%q user=%q", ev.TenantID, ev.TeamID, ev.UserID)
	}
	if ev.Cost.TotalUSD != 18 {
		t.Fatalf("cost not computed: got %+v, want total=18 (1M in @ $3/1M + 1M out @ $15/1M)", ev.Cost)
	}
}

func TestChatHandler_UnpricedModelYieldsZeroCost(t *testing.T) {
	published := make(chan *schema.CallEvent, 1)
	fakeBus := &captureBus{ch: published}

	p := &stubProvider{
		name: "test",
		resp: &schema.ResponseEnvelope{
			ID:      "r1",
			Choices: []schema.Choice{{}},
			Usage:   schema.Usage{PromptTokens: 100, CompletionTokens: 100},
		},
	}
	h := NewChatHandler(fixedRouter(p), fakeBus)
	// A genuinely uncatalogued model name — llama-3.1-8b-instant used to
	// serve this role until it got a real pricingTable entry (see
	// internal/domain/routing/pricing.go), which is exactly the bug this
	// test now guards against regressing the other way.
	w := post(h, `{"model":"totally-fictional-model-not-in-any-catalog","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	ev := readEvent(t, published)
	if ev.Cost.TotalUSD != 0 {
		t.Fatalf("expected zero cost for uncatalogued model, got %+v", ev.Cost)
	}
}

func TestChatHandler_ShadowRoutingPublishesShadowEvent(t *testing.T) {
	published := make(chan *schema.CallEvent, 2)
	fakeBus := &captureBus{ch: published}
	shadowReq := make(chan *schema.RequestEnvelope, 1)

	router := Router(func(model string) (provider.Provider, error) {
		switch model {
		case "gpt-4o":
			return &stubProvider{
				name: "primary",
				resp: &schema.ResponseEnvelope{
					ID:    "primary-resp",
					Model: model,
					Usage: schema.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				},
			}, nil
		case "claude-3-5-sonnet":
			return &stubProvider{
				name: "shadow",
				chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
					shadowReq <- req
					return &schema.ResponseEnvelope{
						ID:    "shadow-resp",
						Model: req.Model,
						Usage: schema.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
					}, nil
				},
			}, nil
		default:
			return nil, fmt.Errorf("unknown model %s", model)
		}
	})

	h := NewChatHandler(router, fakeBus)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IXR-UseCase", "shadow-test")
	req.Header.Set(headerShadowModel, "claude-3-5-sonnet")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	primary := readEvent(t, published)
	if primary.Shadow != nil {
		t.Fatalf("primary event unexpectedly marked shadow: %+v", primary.Shadow)
	}
	shadow := readEvent(t, published)
	if shadow.Shadow == nil {
		t.Fatal("expected shadow metadata")
	}
	if shadow.Model != "claude-3-5-sonnet" || shadow.Provider != "shadow" {
		t.Fatalf("shadow route: got model=%q provider=%q", shadow.Model, shadow.Provider)
	}
	if shadow.Shadow.PrimaryID != "primary-resp" || shadow.Shadow.PrimaryModel != "gpt-4o" {
		t.Fatalf("shadow metadata: got %+v", shadow.Shadow)
	}
	if shadow.TokensIn != 11 || shadow.TokensOut != 7 {
		t.Fatalf("shadow usage: got in=%d out=%d", shadow.TokensIn, shadow.TokensOut)
	}
	gotReq := readRequest(t, shadowReq)
	if gotReq.Model != "claude-3-5-sonnet" {
		t.Fatalf("shadow request model: got %q", gotReq.Model)
	}
}

func TestChatHandler_ShadowRoutingFailureDoesNotAffectPrimary(t *testing.T) {
	published := make(chan *schema.CallEvent, 2)
	fakeBus := &captureBus{ch: published}

	router := Router(func(model string) (provider.Provider, error) {
		if model == "gpt-4o" {
			return &stubProvider{
				name: "primary",
				resp: &schema.ResponseEnvelope{ID: "primary-resp", Model: model},
			}, nil
		}
		return nil, fmt.Errorf("unknown model %s", model)
	})

	h := NewChatHandler(router, fakeBus)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerShadowModel, "missing-shadow-model")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	_ = readEvent(t, published)
	shadow := readEvent(t, published)
	if shadow.Shadow == nil {
		t.Fatal("expected shadow metadata")
	}
	if shadow.Error == "" {
		t.Fatal("expected shadow routing error to be published")
	}
}

func TestChatHandler_ShadowRoutingSameModelSkipped(t *testing.T) {
	published := make(chan *schema.CallEvent, 2)
	fakeBus := &captureBus{ch: published}
	p := &stubProvider{name: "primary", resp: &schema.ResponseEnvelope{ID: "primary-resp", Model: "gpt-4o"}}
	h := NewChatHandler(fixedRouter(p), fakeBus)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerShadowModel, "gpt-4o")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	_ = readEvent(t, published)
	select {
	case ev := <-published:
		t.Fatalf("unexpected second event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func readEvent(t *testing.T, ch <-chan *schema.CallEvent) *schema.CallEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return nil
	}
}

func readRequest(t *testing.T, ch <-chan *schema.RequestEnvelope) *schema.RequestEnvelope {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for request")
		return nil
	}
}

// captureBus implements bus.Bus and captures published events.
type captureBus struct {
	ch chan *schema.CallEvent
}

func (b *captureBus) Publish(_ context.Context, ev *schema.CallEvent) error {
	b.ch <- ev
	return nil
}

func (b *captureBus) Subscribe(_ plugin.EventConsumer) {}
