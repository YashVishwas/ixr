package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// multiRouter dispatches by model name to different stub providers, so a
// chain test can tell which step actually ran.
func multiRouter(byModel map[string]provider.Provider) Router {
	return func(model string) (provider.Provider, error) {
		if p, ok := byModel[model]; ok {
			return p, nil
		}
		return nil, context.DeadlineExceeded
	}
}

func TestChatHandler_ChainRunsStepsInOrderAndFeedsPriorOutput(t *testing.T) {
	var stepAPrompt, stepBPrompt string

	stepA := &stubProvider{name: "provider-a", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Role + ":" + m.Content + "|")
		}
		stepAPrompt = b.String()
		return &schema.ResponseEnvelope{ID: "a1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "draft answer"}}}}, nil
	}}
	stepB := &stubProvider{name: "provider-b", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Role + ":" + m.Content + "|")
		}
		stepBPrompt = b.String()
		return &schema.ResponseEnvelope{ID: "b1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "refined answer"}}}}, nil
	}}

	router := multiRouter(map[string]provider.Provider{"model-a": stepA, "model-b": stepB})
	reg := chain.Registry{"fast-refine": chain.Chain{
		Name:    "fast-refine",
		Models:  []string{"model-a", "model-b"},
		Prompts: []string{"", "Improve the previous answer."},
	}}
	h := NewChatHandler(router, nil, WithChains(reg))

	w := post(h, `{"model":"fast-refine","messages":[{"role":"user","content":"what is a monad?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Choices[0].Message.Content != "refined answer" {
		t.Errorf("final content: got %q, want the last step's output", resp.Choices[0].Message.Content)
	}
	if resp.Model != "fast-refine" {
		t.Errorf("response model: got %q, want the chain name", resp.Model)
	}

	if !strings.Contains(stepAPrompt, "user:what is a monad?") {
		t.Errorf("step A should see the caller's original message, got %q", stepAPrompt)
	}
	if !strings.Contains(stepBPrompt, "assistant:draft answer") || !strings.Contains(stepBPrompt, "user:Improve the previous answer.") {
		t.Errorf("step B should see step A's reply plus its own prompt, got %q", stepBPrompt)
	}
}

func TestChatHandler_ChainAbortsOnStepFailure(t *testing.T) {
	calls := 0
	stepA := &stubProvider{name: "provider-a", err: context.DeadlineExceeded}
	stepB := &stubProvider{name: "provider-b", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		return &schema.ResponseEnvelope{ID: "b1", Choices: []schema.Choice{{}}}, nil
	}}

	router := multiRouter(map[string]provider.Provider{"model-a": stepA, "model-b": stepB})
	reg := chain.Registry{"c": chain.Chain{Name: "c", Models: []string{"model-a", "model-b"}, Prompts: []string{"", ""}}}
	h := NewChatHandler(router, nil, WithChains(reg), WithRetryConfig(fastRetryConfig))

	w := post(h, `{"model":"c","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Errorf("step B calls: got %d, want 0 (chain must abort after step A fails, not skip ahead)", calls)
	}
}

func TestChatHandler_ChainPublishesPerStepCallEvents(t *testing.T) {
	published := make(chan *schema.CallEvent, 2)
	fakeBus := &captureBus{ch: published}

	stepA := &stubProvider{name: "provider-a", resp: &schema.ResponseEnvelope{ID: "a1", Choices: []schema.Choice{{Message: schema.Message{Content: "x"}}}}}
	stepB := &stubProvider{name: "provider-b", resp: &schema.ResponseEnvelope{ID: "b1", Choices: []schema.Choice{{Message: schema.Message{Content: "y"}}}}}
	router := multiRouter(map[string]provider.Provider{"model-a": stepA, "model-b": stepB})
	reg := chain.Registry{"c": chain.Chain{Name: "c", Models: []string{"model-a", "model-b"}, Prompts: []string{"", ""}}}
	h := NewChatHandler(router, fakeBus, WithChains(reg))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"c","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ev1 := readEvent(t, published)
	ev2 := readEvent(t, published)
	if ev1.Model != "model-a" || ev2.Model != "model-b" {
		t.Errorf("expected one CallEvent per real step model, got %q then %q", ev1.Model, ev2.Model)
	}
}

// TestChatHandler_ChainRespectsCircuitBreakerPerStep locks in a fix found
// while dogfooding the demo: chain steps went through routing.Execute (so
// retries worked) but never consulted or updated the circuit breaker, so a
// known-bad model in a chain would be retried and re-tried on every future
// chain run instead of failing fast like a direct request does.
func TestChatHandler_ChainRespectsCircuitBreakerPerStep(t *testing.T) {
	cb := circuitbreaker.NewRegistry(circuitbreaker.Policy{
		SuccessRateThreshold: 0.90,
		WindowDuration:       time.Minute,
		MinRequests:          1,
		HalfOpenAfter:        time.Hour,
		ProbeCount:           1,
	})
	cb.RecordOutcome("model-a", false) // trips a MinRequests:1 breaker

	calls := 0
	stepA := &stubProvider{name: "provider-a", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		return &schema.ResponseEnvelope{ID: "a1", Choices: []schema.Choice{{}}}, nil
	}}
	router := multiRouter(map[string]provider.Provider{"model-a": stepA})
	reg := chain.Registry{"c": chain.Chain{Name: "c", Models: []string{"model-a"}, Prompts: []string{""}}}
	h := NewChatHandler(router, nil, WithChains(reg), WithCBRegistry(cb))

	w := post(h, `{"model":"c","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Errorf("step calls: got %d, want 0 (breaker should short-circuit before calling the provider)", calls)
	}
}

// TestChatHandler_ChainStreamsFinalStep locks in a fix for a bug found via
// dogfooding: a stream:true request against a chains: model used to be
// dispatched to handleChain before ServeHTTP's req.Stream check ever ran,
// so the client always got a plain JSON body back regardless of what it
// asked for — silently breaking any SDK expecting SSE. The final step must
// now honour req.Stream.
func TestChatHandler_ChainStreamsFinalStep(t *testing.T) {
	stepA := &stubProvider{name: "provider-a", resp: &schema.ResponseEnvelope{ID: "a1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "draft answer"}}}}}
	stepB := &stubProvider{name: "provider-b", stream: func(_ context.Context, _ *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
		if err := fn(provider.StreamChunk{ID: "b1", Delta: schema.Message{Content: "refined "}}); err != nil {
			return err
		}
		return fn(provider.StreamChunk{ID: "b1", Delta: schema.Message{Content: "answer"}, Usage: &schema.Usage{PromptTokens: 3, CompletionTokens: 2}})
	}}

	router := multiRouter(map[string]provider.Provider{"model-a": stepA, "model-b": stepB})
	reg := chain.Registry{"fast-refine": chain.Chain{
		Name:    "fast-refine",
		Models:  []string{"model-a", "model-b"},
		Prompts: []string{"", "Improve the previous answer."},
	}}
	h := NewChatHandler(router, nil, WithChains(reg))

	w := post(h, `{"model":"fast-refine","stream":true,"messages":[{"role":"user","content":"what is a monad?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type: got %q, want text/event-stream — chain must honour stream:true", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "refined") || !strings.Contains(body, "answer") {
		t.Errorf("body should contain the streamed final-step deltas, got %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("body should contain [DONE], got %q", body)
	}
}

// TestChatHandler_FusionRunsPanelInParallelThenJudge verifies the fusion
// strategy: all panel models are called with the caller's original
// messages (not each other's output, unlike sequential), and the judge
// model receives all of their answers to synthesize a final one.
func TestChatHandler_FusionRunsPanelInParallelThenJudge(t *testing.T) {
	var judgePrompt string

	panelA := &stubProvider{name: "provider-a", resp: &schema.ResponseEnvelope{ID: "a1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "answer from A"}}}}}
	panelB := &stubProvider{name: "provider-b", resp: &schema.ResponseEnvelope{ID: "b1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "answer from B"}}}}}
	judge := &stubProvider{name: "provider-judge", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Role + ":" + m.Content + "|")
		}
		judgePrompt = b.String()
		return &schema.ResponseEnvelope{ID: "j1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "synthesized answer"}}}}, nil
	}}

	router := multiRouter(map[string]provider.Provider{"model-a": panelA, "model-b": panelB, "model-judge": judge})
	reg := chain.Registry{"debate": chain.Chain{
		Name:     "debate",
		Strategy: chain.StrategyFusion,
		Models:   []string{"model-a", "model-b"},
		Judge:    "model-judge",
	}}
	h := NewChatHandler(router, nil, WithChains(reg))

	w := post(h, `{"model":"debate","messages":[{"role":"user","content":"what is a monad?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Choices[0].Message.Content != "synthesized answer" {
		t.Errorf("final content: got %q, want the judge's synthesis", resp.Choices[0].Message.Content)
	}
	if resp.Model != "debate" {
		t.Errorf("response model: got %q, want the chain name", resp.Model)
	}
	if !strings.Contains(judgePrompt, "user:what is a monad?") {
		t.Errorf("judge should see the caller's original message, got %q", judgePrompt)
	}
	if !strings.Contains(judgePrompt, "answer from A") || !strings.Contains(judgePrompt, "answer from B") {
		t.Errorf("judge should see both panel answers, got %q", judgePrompt)
	}
}

// TestChatHandler_FusionSurvivesPartialPanelFailure verifies fusion's
// resilience advantage over sequential chains: a single panel member
// failing does not abort the request (unlike a sequential step failure) —
// the judge synthesizes from whichever panel members succeeded.
func TestChatHandler_FusionSurvivesPartialPanelFailure(t *testing.T) {
	panelA := &stubProvider{name: "provider-a", err: context.DeadlineExceeded}
	panelB := &stubProvider{name: "provider-b", resp: &schema.ResponseEnvelope{ID: "b1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "answer from B"}}}}}
	judge := &stubProvider{name: "provider-judge", resp: &schema.ResponseEnvelope{ID: "j1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "synthesized from B alone"}}}}}

	router := multiRouter(map[string]provider.Provider{"model-a": panelA, "model-b": panelB, "model-judge": judge})
	reg := chain.Registry{"debate": chain.Chain{
		Name:     "debate",
		Strategy: chain.StrategyFusion,
		Models:   []string{"model-a", "model-b"},
		Judge:    "model-judge",
	}}
	h := NewChatHandler(router, nil, WithChains(reg), WithRetryConfig(fastRetryConfig))

	w := post(h, `{"model":"debate","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (partial panel failure should not abort fusion); body=%s", w.Code, w.Body.String())
	}
	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Choices[0].Message.Content != "synthesized from B alone" {
		t.Errorf("final content: got %q, want the judge's synthesis", resp.Choices[0].Message.Content)
	}
}

// TestChatHandler_FusionFailsWhenWholePanelFails verifies fusion still has
// a terminal failure mode: if every panel member fails, there is nothing
// for the judge to synthesize from, so the request must fail rather than
// call the judge with an empty candidate list.
func TestChatHandler_FusionFailsWhenWholePanelFails(t *testing.T) {
	panelA := &stubProvider{name: "provider-a", err: context.DeadlineExceeded}
	panelB := &stubProvider{name: "provider-b", err: context.DeadlineExceeded}
	judgeCalls := 0
	judge := &stubProvider{name: "provider-judge", chat: func(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		judgeCalls++
		return &schema.ResponseEnvelope{ID: "j1", Choices: []schema.Choice{{}}}, nil
	}}

	router := multiRouter(map[string]provider.Provider{"model-a": panelA, "model-b": panelB, "model-judge": judge})
	reg := chain.Registry{"debate": chain.Chain{
		Name:     "debate",
		Strategy: chain.StrategyFusion,
		Models:   []string{"model-a", "model-b"},
		Judge:    "model-judge",
	}}
	h := NewChatHandler(router, nil, WithChains(reg), WithRetryConfig(fastRetryConfig))

	w := post(h, `{"model":"debate","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", w.Code)
	}
	if judgeCalls != 0 {
		t.Errorf("judge calls: got %d, want 0 (judge must not run with an empty candidate list)", judgeCalls)
	}
}
