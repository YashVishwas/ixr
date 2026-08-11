package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/memory"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func withIdentity(r *http.Request, tenantID, userID string) *http.Request {
	return r.WithContext(identity.WithIdentity(r.Context(), schema.Identity{TenantID: tenantID, UserID: userID}))
}

func TestMemoryHandler_List_ReturnsOnlyCallersOwnEntries(t *testing.T) {
	store := memory.NewMemoryStore("")
	seedMemory(t, store, "acme:alice", "User's name is Alice")
	seedMemory(t, store, "acme:bob", "User's name is Bob")
	h := NewMemoryHandler(store)

	req := withIdentity(httptest.NewRequest(http.MethodGet, "/v1/memory", nil), "acme", "alice")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var body struct {
		Entries []memory.Entry `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Entries) != 1 || body.Entries[0].Content != "User's name is Alice" {
		t.Fatalf("expected only alice's entry, got %+v", body.Entries)
	}
}

func TestMemoryHandler_List_NoEntries_EmptyArrayNotNull(t *testing.T) {
	store := memory.NewMemoryStore("")
	h := NewMemoryHandler(store)

	req := withIdentity(httptest.NewRequest(http.MethodGet, "/v1/memory", nil), "acme", "nobody")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "{\"entries\":[]}\n" {
		t.Errorf("expected an empty array, not null, got %q", got)
	}
}

func TestMemoryHandler_List_MissingUserID_400(t *testing.T) {
	store := memory.NewMemoryStore("")
	h := NewMemoryHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/memory", nil) // no identity in context
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestMemoryHandler_Delete_RemovesOwnEntry(t *testing.T) {
	store := memory.NewMemoryStore("")
	_ = store.Save(t.Context(), memory.Entry{ID: "e1", UserKey: "acme:alice", Category: "name", Content: "Alice"})
	h := NewMemoryHandler(store)

	req := withIdentity(httptest.NewRequest(http.MethodDelete, "/v1/memory/e1", nil), "acme", "alice")
	req.SetPathValue("id", "e1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", w.Code)
	}
	all, _ := store.All(t.Context(), "acme:alice")
	if len(all) != 0 {
		t.Errorf("expected the entry to be deleted, got %+v", all)
	}
}

func TestMemoryHandler_Delete_AnotherUsersEntry_404NotDeleted(t *testing.T) {
	store := memory.NewMemoryStore("")
	_ = store.Save(t.Context(), memory.Entry{ID: "e1", UserKey: "acme:bob", Category: "name", Content: "Bob"})
	h := NewMemoryHandler(store)

	// alice tries to delete bob's entry by ID.
	req := withIdentity(httptest.NewRequest(http.MethodDelete, "/v1/memory/e1", nil), "acme", "alice")
	req.SetPathValue("id", "e1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (alice must not be able to delete bob's entry)", w.Code)
	}
	all, _ := store.All(t.Context(), "acme:bob")
	if len(all) != 1 {
		t.Errorf("expected bob's entry to survive alice's delete attempt, got %+v", all)
	}
}

func TestMemoryHandler_Delete_UnknownID_404(t *testing.T) {
	store := memory.NewMemoryStore("")
	h := NewMemoryHandler(store)

	req := withIdentity(httptest.NewRequest(http.MethodDelete, "/v1/memory/nope", nil), "acme", "alice")
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

func TestMemoryHandler_WrongMethod_405(t *testing.T) {
	store := memory.NewMemoryStore("")
	h := NewMemoryHandler(store)

	req := withIdentity(httptest.NewRequest(http.MethodPost, "/v1/memory", nil), "acme", "alice")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
}
