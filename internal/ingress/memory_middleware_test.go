package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/memory"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func seedMemory(t *testing.T, store *memory.MemoryStore, userKey, content string) {
	t.Helper()
	if err := store.Save(context.Background(), memory.Entry{
		UserKey: userKey, Category: "name", Content: content, Source: "rule",
	}); err != nil {
		t.Fatal(err)
	}
}

func decodedRequest(t *testing.T, r *http.Request) schema.RequestEnvelope {
	t.Helper()
	var req schema.RequestEnvelope
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return req
}

func TestMemoryMiddleware_InjectsOnlyForMatchingUser(t *testing.T) {
	store := memory.NewMemoryStore("")
	seedMemory(t, store, "acme:alice", "User's name is Alice")
	seedMemory(t, store, "acme:bob", "User's name is Bob")

	var got schema.RequestEnvelope
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = decodedRequest(t, r)
	})
	mw := NewMemoryMiddleware(store, 5, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)))
	req = req.WithContext(identity.WithIdentity(req.Context(), schema.Identity{TenantID: "acme", UserID: "alice"}))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Fatalf("expected injected system message, got %+v", got.Messages)
	}
	if !strings.Contains(got.Messages[0].Content, "Alice") {
		t.Errorf("expected Alice's memory injected, got %q", got.Messages[0].Content)
	}
	if strings.Contains(got.Messages[0].Content, "Bob") {
		t.Errorf("bob's memory leaked into alice's request: %q", got.Messages[0].Content)
	}
}

func TestMemoryMiddleware_NoUserIDPassesThroughUnchanged(t *testing.T) {
	store := memory.NewMemoryStore("")
	seedMemory(t, store, "acme:alice", "User's name is Alice")

	var got schema.RequestEnvelope
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = decodedRequest(t, r)
	})
	mw := NewMemoryMiddleware(store, 5, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)))
	// Tenant set but no UserID — should pass through unchanged per the
	// documented "runs only when X-IXR-UserID is set" contract.
	req = req.WithContext(identity.WithIdentity(req.Context(), schema.Identity{TenantID: "acme"}))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("expected passthrough with no injection, got %+v", got.Messages)
	}
}
