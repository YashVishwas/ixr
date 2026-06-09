package identity

import "testing"

func TestResolverResolveHeaders(t *testing.T) {
	got := Resolver{DefaultTenantID: "default"}.Resolve(Headers{
		"x-ixr-user":   {" user-1 "},
		"X-IXR-Tenant": {"tenant-1"},
		"X-IXR-UseCase": {"chat"},
		"X-Request-ID":  {"req-1"},
	})
	if got.UserID != "user-1" || got.TenantID != "tenant-1" || got.UseCaseID != "chat" || got.RequestID != "req-1" {
		t.Fatalf("identity: got %+v", got)
	}
}

func TestResolverDefaultTenant(t *testing.T) {
	got := Resolver{DefaultTenantID: "default"}.Resolve(nil)
	if got.TenantID != "default" {
		t.Fatalf("tenant: got %q, want default", got.TenantID)
	}
}