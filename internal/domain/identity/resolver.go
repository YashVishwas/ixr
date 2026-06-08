package identity

import "strings"

// Headers is a map of header name to values; pass r.Header directly.
type Headers map[string][]string

// Identity carries the normalized caller identity extracted from request headers.
type Identity struct {
	UserID    string
	TenantID  string
	UseCaseID string
	RequestID string
}

// Resolver extracts Identity from request headers.
type Resolver struct {
	DefaultTenantID string
}

// Resolve reads X-IXR-User, X-IXR-Tenant, X-IXR-UseCase, and X-Request-ID headers.
// Header names are matched case-insensitively. Nil headers yield defaults.
func (r Resolver) Resolve(h Headers) Identity {
	get := func(key string) string {
		for k, vs := range h {
			if strings.EqualFold(k, key) && len(vs) > 0 {
				return strings.TrimSpace(vs[0])
			}
		}
		return ""
	}
	tenant := get("X-IXR-Tenant")
	if tenant == "" {
		tenant = r.DefaultTenantID
	}
	return Identity{
		UserID:    get("X-IXR-User"),
		TenantID:  tenant,
		UseCaseID: get("X-IXR-UseCase"),
		RequestID: get("X-Request-ID"),
	}
}
