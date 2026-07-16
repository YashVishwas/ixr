// Package identity normalizes caller identity from request headers.
package identity

import (
	"context"
	"net/http"

	"github.com/YashVishwas/ixr/pkg/schema"
)

type contextKey struct{}

// Resolver extracts Identity from an HTTP request's headers.
type Resolver struct{}

// New returns a Resolver.
func New() *Resolver { return &Resolver{} }

// Resolve extracts Identity from the canonical ixr headers.
// TenantID defaults to "default" when absent.
func (r *Resolver) Resolve(req *http.Request) schema.Identity {
	return schema.Identity{
		TenantID:  firstNonEmpty(req.Header.Get("X-IXR-TenantID"), "default"),
		TeamID:    req.Header.Get("X-IXR-TeamID"),
		UserID:    req.Header.Get("X-IXR-UserID"),
		UseCaseID: req.Header.Get("X-IXR-UseCase"),
	}
}

// WithIdentity stores id in ctx for downstream handlers.
func WithIdentity(ctx context.Context, id schema.Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext retrieves Identity from ctx. Returns zero-value with TenantID="default" if absent.
func FromContext(ctx context.Context) schema.Identity {
	id, ok := ctx.Value(contextKey{}).(schema.Identity)
	if !ok {
		return schema.Identity{TenantID: "default"}
	}
	return id
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
