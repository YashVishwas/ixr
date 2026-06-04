package ingress

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// faultProvider always returns an error simulating an upstream failure.
type faultProvider struct{ code int }

func (f *faultProvider) Name() string { return "fault" }
func (f *faultProvider) Chat(_ context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	return nil, errors.New("upstream unavailable")
}
func (f *faultProvider) Stream(_ context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
	return errors.New("upstream unavailable")
}

// slowProvider simulates a provider that blocks until context is cancelled.
type slowProvider struct{}

func (s *slowProvider) Name() string { return "slow" }
func (s *slowProvider) Chat(ctx context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *slowProvider) Stream(ctx context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestChaos_ProviderError_Returns502(t *testing.T) {
	fp := &faultProvider{}
	h := NewChatHandler(fixedRouter(fp), nil)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChaos_GarbageBody_Returns400(t *testing.T) {
	p := &stubProvider{name: "p", resp: &schema.ResponseEnvelope{ID: "r"}}
	h := NewChatHandler(fixedRouter(p), nil)
	bodies := []string{
		"not json at all",
		`{"model": }`,
		`[1, 2, 3]`,
		string(make([]byte, 1<<16)), // 64KB of zeros
	}
	for _, body := range bodies {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("body %q: expected non-200, got 200", body[:min(len(body), 40)])
		}
	}
}

func TestChaos_WrongMethod_Returns405(t *testing.T) {
	p := &stubProvider{name: "p", resp: &schema.ResponseEnvelope{ID: "r"}}
	h := NewChatHandler(fixedRouter(p), nil)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: want 405, got %d", method, w.Code)
		}
	}
}

func TestChaos_MissingModel_Returns400(t *testing.T) {
	p := &stubProvider{name: "p", resp: &schema.ResponseEnvelope{ID: "r"}}
	h := NewChatHandler(fixedRouter(p), nil)
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
