package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimitMiddleware_UnderLimit_PassesThrough(t *testing.T) {
	m := NewBodyLimitMiddleware(1024)
	var gotBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})
	h := m.Handler(next)

	body := strings.Repeat("a", 100)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if gotBody != body {
		t.Errorf("body: got %d bytes, want %d", len(gotBody), len(body))
	}
}

func TestBodyLimitMiddleware_OverLimit_ReadFails(t *testing.T) {
	m := NewBodyLimitMiddleware(100)
	var readErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	h := m.Handler(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", 1000)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if readErr == nil {
		t.Fatal("expected reading a body over the limit to fail")
	}
}

func TestBodyLimitMiddleware_Disabled_NeverLimits(t *testing.T) {
	m := NewBodyLimitMiddleware(0) // <=0 disables the cap entirely
	var readErr error
	var n int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		readErr = err
		n = len(b)
		w.WriteHeader(http.StatusOK)
	})
	h := m.Handler(next)

	body := strings.Repeat("a", 1_000_000)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if readErr != nil {
		t.Fatalf("expected no error with the cap disabled, got: %v", readErr)
	}
	if n != len(body) {
		t.Errorf("expected the full body to be readable with the cap disabled, got %d of %d bytes", n, len(body))
	}
}

// TestBodyLimitMiddleware_OversizedJSONBody_DegradesToExisting400 proves the
// integration point this middleware relies on: it introduces no new error
// path of its own, it just makes json.Decode fail earlier than it otherwise
// would — every handler's existing "decode failed -> 400
// invalid_request_body" branch already handles that.
func TestBodyLimitMiddleware_OversizedJSONBody_DegradesToExisting400(t *testing.T) {
	m := NewBodyLimitMiddleware(50)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := m.Handler(next)

	oversized := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", 1000) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(oversized))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (existing invalid_request_body handling, unchanged)", w.Code)
	}
}
