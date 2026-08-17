package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// mutatingInterceptor is a guardrail.RequestInterceptor that mutates req in
// place and never blocks — standing in for plugins/compressor without an
// import (avoiding a dependency from internal/ingress on plugins/compressor).
type mutatingInterceptor struct {
	fn func(*schema.RequestEnvelope)
}

func (m *mutatingInterceptor) Name() string { return "test-mutator" }
func (m *mutatingInterceptor) Intercept(_ context.Context, req *schema.RequestEnvelope) error {
	m.fn(req)
	return nil
}

// TestInterceptorMiddleware_MutationReachesNextHandler is the load-bearing
// test for treating guardrail.RequestInterceptor as a full pre-routing
// request transformer (not just an approve/block gate): it proves that an
// interceptor which mutates req in place — rather than only inspecting it —
// has that mutation actually reach the next handler's request body, not
// just its own in-memory copy.
//
// This works because ServeHTTP re-marshals req and replaces r.Body after
// Intercept returns, specifically so a mutation is visible downstream. That
// behavior already existed before this test; this test is what confirms it
// rather than assuming it from reading the source.
func TestInterceptorMiddleware_MutationReachesNextHandler(t *testing.T) {
	var received schema.RequestEnvelope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("next handler: decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	interceptor := &mutatingInterceptor{fn: func(req *schema.RequestEnvelope) {
		req.Messages[0].Content = "mutated"
	}}
	mw := NewInterceptorMiddleware(guardrail.Chain{interceptor}, next)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"original"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if len(received.Messages) != 1 || received.Messages[0].Content != "mutated" {
		t.Fatalf("expected the next handler to see the mutated content, got %+v", received.Messages)
	}
}

// TestInterceptorMiddleware_EmptyChainSkipsBodyRoundTrip guards the existing
// fast path: with no interceptors configured, the body must reach next
// completely untouched — not just equivalent after a decode/re-encode round
// trip (which could reorder keys or drop unknown fields).
func TestInterceptorMiddleware_EmptyChainSkipsBodyRoundTrip(t *testing.T) {
	const body = `{"model":"gpt-4o","messages":[{"role":"user","content":"original"}],"extra_unknown_field":"kept"}`
	var receivedRaw string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, len(body)+16)
		n, _ := r.Body.Read(buf)
		receivedRaw = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	})

	mw := NewInterceptorMiddleware(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if receivedRaw != body {
		t.Errorf("expected byte-for-byte passthrough with an empty chain, got %q", receivedRaw)
	}
}
