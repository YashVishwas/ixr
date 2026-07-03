package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/YashVishwas/ixr/internal/domain/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// InterceptorMiddleware runs a guardrail.Chain before each request reaches the
// cache or chat handler. A non-nil error from any interceptor short-circuits
// the request and returns a 403 to the caller.
type InterceptorMiddleware struct {
	chain guardrail.Chain
	next  http.Handler
}

// NewInterceptorMiddleware wraps next with the given interceptor chain.
// A nil or empty chain is a no-op with zero overhead.
func NewInterceptorMiddleware(chain guardrail.Chain, next http.Handler) *InterceptorMiddleware {
	return &InterceptorMiddleware{chain: chain, next: next}
}

func (m *InterceptorMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(m.chain) == 0 || r.Method != http.MethodPost {
		m.next.ServeHTTP(w, r)
		return
	}

	// Decode the request body to inspect it. The body is re-encoded after
	// interceptors run (which may have redacted content) before passing on.
	var req schema.RequestEnvelope
	body := &bodyCapture{ReadCloser: r.Body}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		// Malformed body — pass through; downstream handler will 400.
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}

	if err := m.chain.Intercept(r.Context(), &req); err != nil {
		guardrail.WriteBlockedResponse(w, err)
		return
	}

	// Re-encode — interceptors may have mutated req.Messages (redact mode).
	encoded, err := json.Marshal(req)
	if err != nil {
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))

	m.next.ServeHTTP(w, r)
}
