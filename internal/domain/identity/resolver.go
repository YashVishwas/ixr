// Package identity normalizes caller identity from request headers.
package identity

import "strings"

const (
	HeaderUserID    = "X-IXR-User"
	HeaderTenantID  = "X-IXR-Tenant"
	HeaderUseCaseID = "X-IXR-UseCase"
	HeaderAPIKeyID  = "X-IXR-Key-ID"
	HeaderRequestID = "X-Request-ID"
)

// Headers is the subset of http.Header used by the resolver.
type Headers map[string][]string

// Identity is the normalized caller identity used by policy, telemetry, and routing.
type Identity struct {
	UserID    string
	TenantID  string
	UseCaseID string
	APIKeyID  string
	RequestID string
}

// Resolver extracts caller identity from trusted ingress headers.
type Resolver struct {
	DefaultTenantID string
}

// Resolve normalizes identity from headers. Missing tenant IDs are assigned the
// configured default so downstream policy keys are stable.
func (r Resolver) Resolve(headers Headers) Identity {
	id := Identity{
		UserID:    first(headers, HeaderUserID),
		TenantID:  first(headers, HeaderTenantID),
		UseCaseID: first(headers, HeaderUseCaseID),
		APIKeyID:  first(headers, HeaderAPIKeyID),
		RequestID: first(headers, HeaderRequestID),
	}
	if id.TenantID == "" {
		id.TenantID = r.DefaultTenantID
	}
	return id
}

func first(headers Headers, key string) string {
	if headers == nil {
		return ""
	}
	for k, values := range headers {
		if !strings.EqualFold(k, key) {
			continue
		}
		for _, v := range values {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
