// Package guardrail defines the RequestInterceptor interface — the synchronous
// pre-call extension point for inspecting or modifying requests before they
// reach a provider. PII scanning, budget gates, and policy checks all
// implement this interface.
package guardrail

import (
	"context"
	"net/http"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// RequestInterceptor inspects (and optionally mutates) a RequestEnvelope
// before it is forwarded to a provider. Returning a non-nil error blocks
// the request; the HTTP middleware surfaces it as a 403.
type RequestInterceptor interface {
	Name() string
	Intercept(ctx context.Context, req *schema.RequestEnvelope) error
}

// BlockedError is returned by interceptors that want to block a request.
type BlockedError struct {
	Interceptor string
	Category    string
	Message     string
}

func (e *BlockedError) Error() string {
	if e.Category != "" {
		return e.Interceptor + ": blocked — " + e.Category + ": " + e.Message
	}
	return e.Interceptor + ": " + e.Message
}

// Chain runs a slice of RequestInterceptors in order, short-circuiting on
// the first error.
type Chain []RequestInterceptor

func (c Chain) Intercept(ctx context.Context, req *schema.RequestEnvelope) error {
	for _, i := range c {
		if err := i.Intercept(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// WriteBlockedResponse writes a structured 403 JSON response.
func WriteBlockedResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	msg := err.Error()
	if blocked, ok := err.(*BlockedError); ok {
		msg = blocked.Message
	}
	_, _ = w.Write([]byte(`{"error":{"type":"request_blocked","message":"` + jsonEscape(msg) + `"}}`))
}

func jsonEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
