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
// the request; the error message is sent to the caller as a 403 body.
//
// Interceptors must be fast — they run synchronously in the request path.
// Any work that can be deferred to after the response should use EventConsumer.
type RequestInterceptor interface {
	// Name returns a stable identifier used in logs and error responses.
	Name() string
	// Intercept is called once per request after auth and rate-limiting but
	// before cache lookup and the provider call. Return nil to allow the
	// request to proceed.
	Intercept(ctx context.Context, req *schema.RequestEnvelope) error
}

// BlockedError is returned by interceptors that want to block a request.
// The HTTP middleware surfaces it as a structured 403 with the category that
// triggered the block, making it auditable in access logs.
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

// Chain runs a slice of RequestInterceptors in order. The first interceptor
// to return a non-nil error short-circuits the chain.
type Chain []RequestInterceptor

// Intercept runs each interceptor in order and returns the first error.
func (c Chain) Intercept(ctx context.Context, req *schema.RequestEnvelope) error {
	for _, i := range c {
		if err := i.Intercept(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// WriteBlockedResponse writes a structured 403 to w from a BlockedError or a
// generic interceptor error. Called by the ingress middleware.
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
