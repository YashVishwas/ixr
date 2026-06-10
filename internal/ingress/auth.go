package ingress

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt/v5"

	cfgpkg "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// AuthMiddleware validates inbound API keys, JWT tokens, and mTLS client certs.
type AuthMiddleware struct {
	mu       sync.RWMutex
	cfg      cfgpkg.AuthConfig
	keyIndex map[string]cfgpkg.APIKeyEntry
	resolver *identity.Resolver
}

// NewAuthMiddleware creates a middleware from the given AuthConfig.
func NewAuthMiddleware(cfg cfgpkg.AuthConfig, resolver *identity.Resolver) *AuthMiddleware {
	m := &AuthMiddleware{resolver: resolver}
	m.Reload(cfg)
	return m
}

// Reload updates the middleware configuration atomically (called on hot reload).
func (m *AuthMiddleware) Reload(cfg cfgpkg.AuthConfig) {
	idx := make(map[string]cfgpkg.APIKeyEntry, len(cfg.APIKeys))
	for _, k := range cfg.APIKeys {
		idx[k.Key] = k
	}
	m.mu.Lock()
	m.cfg = cfg
	m.keyIndex = idx
	m.mu.Unlock()
}

// Handler wraps next, performing auth before passing through.
func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		cfg := m.cfg
		keyIndex := m.keyIndex
		m.mu.RUnlock()

		if cfg.DisableAuth {
			id := m.resolver.Resolve(r)
			next.ServeHTTP(w, r.WithContext(identity.WithIdentity(r.Context(), id)))
			return
		}

		id, err := authenticate(r, cfg, keyIndex)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithIdentity(r.Context(), id)))
	})
}

func authenticate(r *http.Request, cfg cfgpkg.AuthConfig, keyIndex map[string]cfgpkg.APIKeyEntry) (schema.Identity, error) {
	// 1. mTLS — client cert CN becomes TenantID.
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		cn := r.TLS.PeerCertificates[0].Subject.CommonName
		return schema.Identity{TenantID: cn, UseCaseID: r.Header.Get("X-IXR-UseCase")}, nil
	}

	// 2. API key — check Authorization: Bearer <non-JWT> or X-API-Key header.
	rawKey := extractBearerKey(r)
	if rawKey == "" {
		rawKey = r.Header.Get("X-API-Key")
	}
	if rawKey != "" {
		entry, ok := keyIndex[rawKey]
		if ok {
			return schema.Identity{
				TenantID:  entry.TenantID,
				UseCaseID: r.Header.Get("X-IXR-UseCase"),
			}, nil
		}
		// Key provided but not found — only reject if JWT is also unconfigured.
		if cfg.JWT.Secret == "" && cfg.JWT.JWKSURL == "" {
			return schema.Identity{}, errors.New("invalid API key")
		}
	}

	// 3. JWT — Authorization: Bearer <token>.
	if cfg.JWT.Secret != "" || cfg.JWT.JWKSURL != "" {
		token := extractBearerToken(r)
		if token == "" {
			return schema.Identity{}, errors.New("missing authentication credentials")
		}
		return validateJWT(token, cfg.JWT)
	}

	return schema.Identity{}, errors.New("missing authentication credentials")
}

// extractBearerKey returns the value of the Authorization header if it looks like
// an API key (no dots — JWTs always contain dots separating header.payload.signature).
func extractBearerKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	val := strings.TrimPrefix(auth, "Bearer ")
	if strings.Contains(val, ".") {
		return "" // JWT
	}
	return val
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func validateJWT(tokenStr string, cfg cfgpkg.JWTConfig) (schema.Identity, error) {
	var keyfunc jwt.Keyfunc

	if cfg.Secret != "" {
		secret := []byte(cfg.Secret)
		keyfunc = func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return secret, nil
		}
	} else {
		keyfunc = func(t *jwt.Token) (any, error) {
			return fetchJWKSKey(cfg.JWKSURL, t)
		}
	}

	opts := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(cfg.Audience))
	}

	parsed, err := jwt.Parse(tokenStr, keyfunc, opts...)
	if err != nil {
		return schema.Identity{}, errors.New("invalid token")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return schema.Identity{}, errors.New("invalid token claims")
	}

	id := schema.Identity{}
	if v, ok := claims["tenant_id"].(string); ok {
		id.TenantID = v
	}
	if id.TenantID == "" {
		if v, ok := claims["sub"].(string); ok {
			id.TenantID = v
		}
	}
	if id.TenantID == "" {
		id.TenantID = "default"
	}
	return id, nil
}

// fetchJWKSKey retrieves the matching RSA public key from a JWKS endpoint.
func fetchJWKSKey(jwksURL string, t *jwt.Token) (any, error) {
	if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, errors.New("unexpected signing method for JWKS auth")
	}

	resp, err := http.Get(jwksURL) //nolint:gosec
	if err != nil {
		return nil, errors.New("jwks: fetch failed")
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := jsonDecodeBody(resp.Body, &jwks); err != nil {
		return nil, errors.New("jwks: decode failed")
	}

	kid, _ := t.Header["kid"].(string)
	for _, k := range jwks.Keys {
		if k.Kid == kid || kid == "" {
			return parseRSAPublicKey(k.N, k.E)
		}
	}
	return nil, errors.New("jwks: no matching key")
}

func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	// Pad eBytes to 4 bytes for binary.BigEndian.Uint32.
	padded := make([]byte, 4)
	copy(padded[4-len(eBytes):], eBytes)
	e := int(binary.BigEndian.Uint32(padded))

	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func jsonDecodeBody(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// NewServerTLS creates a TLS-enabled server.
// certFile and keyFile are PEM paths. clientCA, if non-empty, enables mTLS.
func NewServerTLS(port int, mux *http.ServeMux, certFile, keyFile, clientCA string) (*Server, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	if clientCA != "" {
		caPEM, err := os.ReadFile(clientCA)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("failed to parse client CA cert")
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return &Server{port: port, mux: mux, tlsCfg: tlsCfg}, nil
}
