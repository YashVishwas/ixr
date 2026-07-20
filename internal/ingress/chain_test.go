package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/chain"
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
