package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeVerifier struct {
	token string
}

func (v fakeVerifier) Verify(_ context.Context, token string) (AuthPrincipal, error) {
	if token != v.token {
		return AuthPrincipal{}, context.Canceled
	}
	return AuthPrincipal{Subject: "jwt-sub"}, nil
}

func TestAuthMiddlewareAllowsAPIKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.KeyID != "key-1" {
			t.Fatalf("principal: got %+v ok=%v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := authMiddleware(next, AuthConfig{APIKeys: map[string]APIKey{"key-1": {Secret: "secret"}}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-IXR-API-Key", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
}

func TestAuthMiddlewareAllowsBearer(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.Subject != "jwt-sub" {
			t.Fatalf("principal: got %+v ok=%v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := authMiddleware(next, AuthConfig{JWTVerifier: fakeVerifier{token: "good"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
}

func TestAuthMiddlewareRejectsMissingCredentials(t *testing.T) {
	h := authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not be called")
	}), AuthConfig{APIKeys: map[string]APIKey{"key-1": {Secret: "secret"}}})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", w.Code)
	}
}
