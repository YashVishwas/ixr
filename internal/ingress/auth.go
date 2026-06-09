package ingress

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type authContextKey struct{}

// AuthPrincipal is the caller identity established by ingress auth.
type AuthPrincipal struct {
	Subject string
	KeyID   string
	Scopes  []string
}

// JWTVerifier verifies a bearer token and returns its principal.
type JWTVerifier interface {
	Verify(ctx context.Context, token string) (AuthPrincipal, error)
}

// AuthConfig configures ingress authentication.
type AuthConfig struct {
	APIKeys     map[string]string // key id -> secret
	JWTVerifier JWTVerifier
}

// authMiddleware verifies inbound API keys and JWT tokens.
func authMiddleware(next http.Handler, cfg AuthConfig) http.Handler {
	if len(cfg.APIKeys) == 0 && cfg.JWTVerifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := authenticateAPIKey(r, cfg.APIKeys); ok {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
			return
		}
		if cfg.JWTVerifier != nil {
			if principal, ok := authenticateBearer(r, cfg.JWTVerifier); ok {
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid ixr credentials")
	})
}

// WithPrincipal stores an authenticated principal on ctx.
func WithPrincipal(ctx context.Context, principal AuthPrincipal) context.Context {
	return context.WithValue(ctx, authContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated principal when present.
func PrincipalFromContext(ctx context.Context) (AuthPrincipal, bool) {
	principal, ok := ctx.Value(authContextKey{}).(AuthPrincipal)
	return principal, ok
}

func authenticateAPIKey(r *http.Request, keys map[string]string) (AuthPrincipal, bool) {
	if len(keys) == 0 {
		return AuthPrincipal{}, false
	}
	raw := strings.TrimSpace(r.Header.Get("X-IXR-API-Key"))
	if raw == "" {
		return AuthPrincipal{}, false
	}
	for id, secret := range keys {
		if subtle.ConstantTimeCompare([]byte(raw), []byte(secret)) == 1 {
			return AuthPrincipal{Subject: id, KeyID: id}, true
		}
	}
	return AuthPrincipal{}, false
}

func authenticateBearer(r *http.Request, verifier JWTVerifier) (AuthPrincipal, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return AuthPrincipal{}, false
	}
	token := strings.TrimSpace(raw[len("bearer "):])
	if token == "" {
		return AuthPrincipal{}, false
	}
	principal, err := verifier.Verify(r.Context(), token)
	return principal, err == nil
}