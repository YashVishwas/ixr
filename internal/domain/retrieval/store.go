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
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Backend is the storage medium behind a Store. The default (NewStore) is
// in-memory and single-instance only: an ID minted by one ixr replica can't
// be resolved by another sitting behind the same load balancer, so the
// reversibility guarantee silently degrades to "content not found" the
// moment there's more than one replica. NewStoreWithBackend accepts a
// shared implementation instead — see
// internal/adapters/store/retrievalstore for a Redis-backed one — so the
// guarantee holds across a multi-instance deployment too.
//
// Backend methods return an error rather than panicking or blocking so a
// backend outage degrades to a miss (matching the RFC's request-path
// latency-budget constraint: never a hang), not a stuck request.
type Backend interface {
	Put(ctx context.Context, id, content string, ttl time.Duration) error
	Get(ctx context.Context, id string) (content string, ok bool, err error)
}

// Store holds original content keyed by a Put-generated ID, with TTL
// eviction, behind a pluggable Backend. The default backend (NewStore) is
// in-memory only, matching the rest of ixr's default stores
// (internal/domain/cache.Memory, internal/domain/memory.MemoryStore) — no
// persistence across restarts, which is fine for a retrieval window meant
// to last one active conversation, not indefinitely.
type Store struct {
	backend Backend
}

// NewStore creates a Store backed by an in-memory map. maxSize<=0 means
// unlimited. Single-instance only — see Backend's doc comment.
func NewStore(maxSize int) *Store {
	return &Store{backend: newMemoryBackend(maxSize)}
}

// NewStoreWithBackend creates a Store backed by an arbitrary Backend
// implementation (e.g. a shared, multi-instance-safe one).
func NewStoreWithBackend(b Backend) *Store {
	return &Store{backend: b}
}

// Put stores content and returns an ID for later retrieval via Get. ttl<=0
// means the entry never expires (still subject to the backend's own
// capacity/eviction policy, if any).
//
// The ID's random suffix (not a per-process counter) is deliberate: with a
// shared backend, two ixr replicas minting IDs independently and
// concurrently must never collide, or one replica's Get could resolve to
// another conversation's content.
func (s *Store) Put(ctx context.Context, content string, ttl time.Duration) string {
	id := "ret_" + randomID()
	// The in-memory backend never errors; a shared backend erroring on
	// write degrades to "this entry just won't be retrievable later" (a
	// Get miss), not a failed request — consistent with Get's own
	// error-to-miss degradation below.
	_ = s.backend.Put(ctx, id, content, ttl)
	return id
}

// Get returns the content stored under id, or ok=false if it was never
// stored, has expired, has been evicted, or the backend is unreachable.
func (s *Store) Get(ctx context.Context, id string) (string, bool) {
	content, ok, err := s.backend.Get(ctx, id)
	if err != nil {
		return "", false
	}
	return content, ok
}

// Len returns the current entry count, for monitoring/tests. Backends that
// don't track a cheap count (e.g. a shared Redis backend, where "how many
// keys across the whole deployment" isn't a meaningful per-instance number)
// return -1.
func (s *Store) Len() int {
	if l, ok := s.backend.(interface{ Len() int }); ok {
		return l.Len()
	}
	return -1
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// memoryBackend is the default in-memory Backend implementation.
type memoryBackend struct {
	mu      sync.Mutex
	entries map[string]memEntry
	maxSize int
}

type memEntry struct {
	content    string
	expiresAt  time.Time
	insertedAt time.Time
}

func newMemoryBackend(maxSize int) *memoryBackend {
	return &memoryBackend{entries: make(map[string]memEntry), maxSize: maxSize}
}

func (b *memoryBackend) Put(_ context.Context, id, content string, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxSize > 0 && len(b.entries) >= b.maxSize {
		b.evictOldestLocked()
	}
	b.entries[id] = memEntry{content: content, expiresAt: exp, insertedAt: time.Now()}
	return nil
}

func (b *memoryBackend) Get(_ context.Context, id string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[id]
	if !ok {
		return "", false, nil
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(b.entries, id)
		return "", false, nil
	}
	return e.content, true, nil
}

func (b *memoryBackend) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// evictOldestLocked drops the entry with the earliest insertedAt to make
// room. Callers must hold b.mu.
func (b *memoryBackend) evictOldestLocked() {
	var oldestID string
	var oldestAt time.Time
	for id, e := range b.entries {
		if oldestID == "" || e.insertedAt.Before(oldestAt) {
			oldestID = id
			oldestAt = e.insertedAt
		}
	}
	if oldestID != "" {
		delete(b.entries, oldestID)
	}
}
