package ingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cfgpkg "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// makeJWT builds a minimal HS256 JWT with the given claims.
func makeJWT(secret string, claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	sig := hmacSign(secret, header+"."+payload)
	return header + "." + payload + "." + sig
}

func hmacSign(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func authMWWithKeys(keys ...cfgpkg.APIKeyEntry) *AuthMiddleware {
	return NewAuthMiddleware(cfgpkg.AuthConfig{APIKeys: keys}, &identity.Resolver{})
}

func authMWWithJWT(secret string) *AuthMiddleware {
	return NewAuthMiddleware(cfgpkg.AuthConfig{JWT: cfgpkg.JWTConfig{Secret: secret}}, &identity.Resolver{})
}

func recordReq(mw *AuthMiddleware, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mw.Handler(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		id := identity.FromContext(r.Context())
		_, _ = rw.Write([]byte(id.TenantID))
	})).ServeHTTP(w, req)
	return w
}

func TestAuth_ValidAPIKey_Bearer(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "sk-ixr-test", TenantID: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer sk-ixr-test")
	w := recordReq(mw, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "acme" {
		t.Fatalf("want tenant 'acme', got %q", w.Body.String())
	}
}

func TestAuth_ValidAPIKey_XHeader(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "sk-ixr-test", TenantID: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-API-Key", "sk-ixr-test")
	w := recordReq(mw, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAuth_InvalidAPIKey(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "sk-ixr-test", TenantID: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-API-Key", "wrong-key")
	w := recordReq(mw, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuth_MissingCredentials(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "sk-ixr-test", TenantID: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := recordReq(mw, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	// Response should be OpenAI-compatible error body.
	var errBody map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errBody); err != nil {
		t.Fatal("response not valid JSON")
	}
	if _, ok := errBody["error"]; !ok {
		t.Fatal("missing 'error' field in response")
	}
}

func TestAuth_DisableAuth_Passthrough(t *testing.T) {
	mw := NewAuthMiddleware(cfgpkg.AuthConfig{DisableAuth: true}, &identity.Resolver{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := recordReq(mw, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAuth_ValidJWT_HS256(t *testing.T) {
	secret := "super-secret-key"
	mw := authMWWithJWT(secret)
	token := makeJWT(secret, map[string]any{"tenant_id": "jwt-tenant"})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := recordReq(mw, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "jwt-tenant" {
		t.Fatalf("want tenant 'jwt-tenant', got %q", w.Body.String())
	}
}

func TestAuth_InvalidJWT(t *testing.T) {
	mw := authMWWithJWT("secret")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	w := recordReq(mw, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuth_JWT_SubFallback(t *testing.T) {
	secret := "key"
	mw := authMWWithJWT(secret)
	// No tenant_id claim — should fall back to sub.
	token := makeJWT(secret, map[string]any{"sub": "user-123"})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := recordReq(mw, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if w.Body.String() != "user-123" {
		t.Fatalf("want 'user-123', got %q", w.Body.String())
	}
}

func TestAuth_Reload(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "old-key", TenantID: "t1"})

	// Old key works.
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-API-Key", "old-key")
	if w := recordReq(mw, req); w.Code != http.StatusOK {
		t.Fatal("old key should work before reload")
	}

	// Reload with new config.
	mw.Reload(cfgpkg.AuthConfig{APIKeys: []cfgpkg.APIKeyEntry{{Key: "new-key", TenantID: "t2"}}})

	// Old key now fails.
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-API-Key", "old-key")
	if w := recordReq(mw, req); w.Code != http.StatusUnauthorized {
		t.Fatal("old key should fail after reload")
	}

	// New key works.
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-API-Key", "new-key")
	if w := recordReq(mw, req); w.Code != http.StatusOK {
		t.Fatal("new key should work after reload")
	}
}

// Ensure that identity injected by auth is readable by downstream handlers.
func TestAuth_IdentityPropagation(t *testing.T) {
	mw := authMWWithKeys(cfgpkg.APIKeyEntry{Key: "sk-key", TenantID: "tenant-xyz"})
	var captured schema.Identity
	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = identity.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "sk-key")
	req.Header.Set("X-IXR-UseCase", "search")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured.TenantID != "tenant-xyz" {
		t.Fatalf("want TenantID 'tenant-xyz', got %q", captured.TenantID)
	}
	if captured.UseCaseID != "search" {
		t.Fatalf("want UseCaseID 'search', got %q", captured.UseCaseID)
	}
}
