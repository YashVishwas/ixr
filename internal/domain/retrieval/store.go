// Package retrieval provides shared infrastructure for reversible
// compression — the Headroom CCR pattern: a compressor stores the original,
// uncompressed content of something it shortened, and gives the model a way
// to ask for it back if the compressed version turns out not to be enough.
//
// plugins/compressor writes into a Store when it compresses a message
// destructively enough to risk losing information (truncation, not just
// harmless whitespace collapsing) and marks the compressed content with a
// Marker referencing the stored ID. internal/ingress's ChatHandler reads
// from the same Store when it sees the model call ToolName, resolving it
// internally — the model gets the original content back and the caller
// never sees the intermediate exchange. See internal/ingress/chat.go's
// resolveRetrieval for that side.
package retrieval

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Store holds original content keyed by a Put-generated ID, with TTL
// eviction. In-memory only, matching the rest of ixr's default stores
// (internal/domain/cache.Memory, internal/domain/memory.MemoryStore) — no
// persistence across restarts, which is fine for a retrieval window meant
// to last one active conversation, not indefinitely.
type Store struct {
	mu      sync.Mutex
	entries map[string]entry
	maxSize int
	counter atomic.Uint64
}

type entry struct {
	content   string
	expiresAt time.Time
}

// NewStore creates a Store. maxSize<=0 means unlimited.
func NewStore(maxSize int) *Store {
	return &Store{entries: make(map[string]entry), maxSize: maxSize}
}

// Put stores content and returns an ID for later retrieval via Get. ttl<=0
// means the entry never expires (still subject to maxSize eviction).
func (s *Store) Put(_ context.Context, content string, ttl time.Duration) string {
	id := fmt.Sprintf("ret_%d", s.counter.Add(1))

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxSize > 0 && len(s.entries) >= s.maxSize {
		s.evictOldestLocked()
	}
	s.entries[id] = entry{content: content, expiresAt: exp}
	return id
}

// Get returns the content stored under id, or ok=false if it was never
// stored, has expired, or has been evicted.
func (s *Store) Get(_ context.Context, id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return "", false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(s.entries, id)
		return "", false
	}
	return e.content, true
}

// Len returns the current entry count, for monitoring/tests.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// evictOldestLocked drops one entry to make room. IDs are monotonically
// increasing (ret_1, ret_2, ...), so the lexicographically-and-numerically
// smallest ID is always the oldest — no separate insertion-order tracking
// needed. Callers must hold s.mu.
func (s *Store) evictOldestLocked() {
	var oldestID string
	var oldestNum uint64 = ^uint64(0)
	for id := range s.entries {
		var n uint64
		if _, err := fmt.Sscanf(id, "ret_%d", &n); err != nil {
			continue
		}
		if n < oldestNum {
			oldestNum = n
			oldestID = id
		}
	}
	if oldestID != "" {
		delete(s.entries, oldestID)
	}
}
