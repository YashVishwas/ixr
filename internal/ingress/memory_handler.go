package ingress

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/memory"
)

// MemoryHandler handles GET /v1/memory and DELETE /v1/memory/{id} — the
// Future Work item this RFC listed but never built: a way for a user to
// inspect and correct what ixr has automatically remembered about them.
// Also the practical answer to Open Question 8 (memory staleness/
// contradiction): there's no automatic contradiction detection, but a
// caller (or an application built on top of ixr) can now delete a stale
// entry directly rather than living with it until it expires on its own.
//
// Scoped to the identity resolved from the request the same way
// MemoryMiddleware/RetrieveMemoriesForContext already are (memoryUserKey)
// — a caller only ever sees or deletes their own entries.
type MemoryHandler struct {
	store memory.Store
}

// NewMemoryHandler creates a handler backed by store — the same instance
// MemoryMiddleware reads from and plugins/memory writes to.
func NewMemoryHandler(store memory.Store) *MemoryHandler {
	return &MemoryHandler{store: store}
}

func (h *MemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := identity.FromContext(r.Context())
	userKey := memoryUserKey(id)
	if userKey == "" {
		writeError(w, http.StatusBadRequest, "missing_user_id", "X-IXR-UserID header is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.list(w, r, userKey)
	case http.MethodDelete:
		h.delete(w, r, userKey)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and DELETE are supported")
	}
}

func (h *MemoryHandler) list(w http.ResponseWriter, r *http.Request, userKey string) {
	entries, err := h.store.All(r.Context(), userKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", "failed to read memory entries")
		return
	}
	if entries == nil {
		entries = []memory.Entry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Entries []memory.Entry `json:"entries"`
	}{Entries: entries})
}

func (h *MemoryHandler) delete(w http.ResponseWriter, r *http.Request, userKey string) {
	entryID := r.PathValue("id")
	if entryID == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "memory entry id is required")
		return
	}

	err := h.store.Delete(r.Context(), userKey, entryID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no memory entry with that id")
	default:
		writeError(w, http.StatusInternalServerError, "store_error", "failed to delete memory entry")
	}
}
