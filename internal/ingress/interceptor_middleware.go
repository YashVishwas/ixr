package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// InterceptorMiddleware runs a guardrail.Chain before each request reaches the
// cache or chat handler. A non-nil error short-circuits with a 403.
// Blocked requests are published to the bus when one is configured.
type InterceptorMiddleware struct {
	chain guardrail.Chain
	bus   bus.Bus
	next  http.Handler
}

// NewInterceptorMiddleware wraps next with the given interceptor chain.
func NewInterceptorMiddleware(chain guardrail.Chain, next http.Handler) *InterceptorMiddleware {
	return &InterceptorMiddleware{chain: chain, next: next}
}

// WithBus attaches an event bus so blocked requests are auditable.
func (m *InterceptorMiddleware) WithBus(b bus.Bus) *InterceptorMiddleware {
	m.bus = b
	return m
}

func (m *InterceptorMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(m.chain) == 0 || r.Method != http.MethodPost {
		m.next.ServeHTTP(w, r)
		return
	}

	var req schema.RequestEnvelope
	body := &bodyCapture{ReadCloser: r.Body}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}

	if err := m.chain.Intercept(r.Context(), &req); err != nil {
		guardrail.WriteBlockedResponse(w, err)
		if m.bus != nil {
			id := identity.FromContext(r.Context())
			_ = m.bus.Publish(r.Context(), &schema.CallEvent{
				Timestamp: time.Now(),
				TenantID:  id.TenantID,
				UseCaseID: r.Header.Get("X-IXR-UseCase"),
				Model:     req.Model,
				Request:   req,
				Error:     err.Error(),
			})
		}
		return
	}

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
