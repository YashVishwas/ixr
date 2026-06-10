package identity

import (
	"net/http"
	"testing"
)

func TestResolverResolveHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-IXR-UserID", "user-1")
	req.Header.Set("X-IXR-TenantID", "tenant-1")
	req.Header.Set("X-IXR-UseCase", "chat")

	got := New().Resolve(req)
	if got.UserID != "user-1" || got.TenantID != "tenant-1" || got.UseCaseID != "chat" {
		t.Fatalf("identity: got %+v", got)
	}
}

func TestResolverDefaultTenant(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	got := New().Resolve(req)
	if got.TenantID != "default" {
		t.Fatalf("tenant: got %q, want default", got.TenantID)
	}
}
