// Package observability provides request ID propagation, Prometheus metrics,
// and OpenTelemetry tracing for the ixr proxy.
package observability

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey int

const reqIDKey contextKey = 1

// RequestIDMiddleware attaches a unique X-Request-ID to every inbound request.
// If the caller already sent X-Request-ID it is used as-is; otherwise a new UUID is generated.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// WithRequestID attaches id to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, reqIDKey, id)
}

// RequestIDFromContext returns the request ID stored by RequestIDMiddleware, or "".
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(reqIDKey).(string)
	return v
}
