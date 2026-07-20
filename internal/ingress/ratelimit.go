package ingress

import (
	"fmt"
	"net/http"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/policy"
)

// RateLimitMiddleware enforces per-identity sliding-window rate limits.
// On limit: returns 429 with Retry-After header and OpenAI-compatible error body.
type RateLimitMiddleware struct {
	limiter policy.RateLimiter
}

// NewRateLimitMiddleware creates middleware backed by the given limiter.
func NewRateLimitMiddleware(limiter policy.RateLimiter) *RateLimitMiddleware {
	return &RateLimitMiddleware{limiter: limiter}
}

// Handler wraps next, enforcing rate limits before passing through.
func (m *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := identity.FromContext(r.Context())

		// Rough token estimate from Content-Length; 4 chars ≈ 1 token.
		tokenEstimate := int(r.ContentLength) / 4

		d := m.limiter.Check(r.Context(), id, tokenEstimate)
		if !d.Allowed {
			if d.RetryAfter > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", d.RetryAfter.Seconds()))
			}
			writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded",
				"request rate limit exceeded; please retry after the indicated delay")
			return
		}

		rw := &responseCapture{ResponseWriter: w}
		next.ServeHTTP(rw, r)

		// Record actual tokens from the response (approximate from body size).
		actualTokens := rw.bytesWritten / 4
		m.limiter.Record(r.Context(), id, actualTokens)
	})
}

// responseCapture wraps ResponseWriter to count bytes written.
type responseCapture struct {
	http.ResponseWriter
	bytesWritten int
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	n, err := rc.ResponseWriter.Write(b)
	rc.bytesWritten += n
	return n, err
}

// Flush forwards to the underlying ResponseWriter's Flusher so streaming
// responses still work when passed through this wrapper. Without this,
// wrapping in responseCapture silently breaks the http.Flusher type
// assertion the SSE handler relies on for every request, not just
// rate-limited ones.
func (rc *responseCapture) Flush() {
	if f, ok := rc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
