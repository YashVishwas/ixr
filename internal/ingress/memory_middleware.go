package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/memory"
	memoryplugin "github.com/YashVishwas/ixr/plugins/memory"
	"github.com/YashVishwas/ixr/pkg/schema"
)

const defaultMemoryTopK = 5

// MemoryMiddleware retrieves stored user memories and prepends them as a
// system message before the request reaches the provider. Runs only when
// X-IXR-UserID is set — anonymous requests pass through unchanged.
//
// Memories are injected once per new conversation context. When used alongside
// SessionMiddleware, place MemoryMiddleware outside (earlier in the chain) so
// memories are available before session history is appended.
type MemoryMiddleware struct {
	store memory.Store
	topK  int
	next  http.Handler
}

// NewMemoryMiddleware creates a middleware that injects user memories.
// topK controls how many memories are retrieved (default 5).
func NewMemoryMiddleware(store memory.Store, topK int, next http.Handler) *MemoryMiddleware {
	if topK <= 0 {
		topK = defaultMemoryTopK
	}
	return &MemoryMiddleware{store: store, topK: topK, next: next}
}

func (m *MemoryMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := identity.FromContext(r.Context())

	// Only inject when we have a concrete user identity.
	userKey := memoryUserKey(id)
	if userKey == "" || r.Method != http.MethodPost {
		m.next.ServeHTTP(w, r)
		return
	}

	entries, err := m.store.Recent(r.Context(), userKey, m.topK)
	if err != nil || len(entries) == 0 {
		m.next.ServeHTTP(w, r)
		return
	}

	// Decode the request body.
	var req schema.RequestEnvelope
	body := &bodyCapture{ReadCloser: r.Body}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}

	// Prepend a system message with the formatted memories.
	memMsg := memoryplugin.FormatMemories(entries)
	if memMsg != "" {
		req.Messages = append(
			[]schema.Message{{Role: "system", Content: memMsg}},
			req.Messages...,
		)
		slog.Debug("memory: injected user context", "user_key", userKey, "facts", len(entries))
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		r.Body = body.replay()
		m.next.ServeHTTP(w, r)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))

	m.next.ServeHTTP(w, r)
}

// memoryUserKey returns a store key for the identity, or "" if the identity
// is anonymous (no concrete TenantID or UserID to scope memories to).
func memoryUserKey(id schema.Identity) string {
	if id.TenantID == "" || id.TenantID == "default" {
		return ""
	}
	return id.TenantID
}

// RetrieveMemoriesForContext is a helper for SessionMiddleware integration:
// retrieves memories for a user and formats them as a system message string.
// Returns "" when no memories are found or the store errors.
func RetrieveMemoriesForContext(ctx context.Context, store memory.Store, id schema.Identity, topK int) string {
	key := memoryUserKey(id)
	if key == "" {
		return ""
	}
	entries, err := store.Recent(ctx, key, topK)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return memoryplugin.FormatMemories(entries)
}
