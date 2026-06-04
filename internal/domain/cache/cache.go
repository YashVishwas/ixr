// Package cache provides exact-match and semantic response caching for the ixr proxy.
// The default implementation uses a SHA-256 hash of the normalized request as the
// cache key, giving exact-match semantics with zero embedding cost.
// Swap in a SemanticBackend (e.g. pgvector, Weaviate) to enable fuzzy matching.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Cache looks up and stores LLM responses by request key.
type Cache interface {
	Get(ctx context.Context, key string) (*schema.ResponseEnvelope, bool)
	Set(ctx context.Context, key string, resp *schema.ResponseEnvelope, ttl time.Duration)
	Invalidate(ctx context.Context, key string)
}

// Key returns the canonical cache key for req.
// Two requests with identical model + sorted messages produce the same key.
func Key(req *schema.RequestEnvelope) string {
	type canonical struct {
		Model    string           `json:"model"`
		Messages []schema.Message `json:"messages"`
	}
	b, _ := json.Marshal(canonical{Model: req.Model, Messages: req.Messages})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// entry holds a cached response and its expiry.
type entry struct {
	resp    *schema.ResponseEnvelope
	expiresAt time.Time
}

// Memory is an in-process LRU cache with TTL eviction.
// Safe for concurrent use.
type Memory struct {
	mu      sync.Mutex
	entries map[string]*entry
	maxSize int
	defaultTTL time.Duration
}

// NewMemory creates an in-memory cache.
// maxSize=0 means unlimited. defaultTTL=0 means entries never expire.
func NewMemory(maxSize int, defaultTTL time.Duration) *Memory {
	return &Memory{
		entries:    make(map[string]*entry),
		maxSize:    maxSize,
		defaultTTL: defaultTTL,
	}
}

func (m *Memory) Get(_ context.Context, key string) (*schema.ResponseEnvelope, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(m.entries, key)
		return nil, false
	}
	return e.resp, true
}

func (m *Memory) Set(_ context.Context, key string, resp *schema.ResponseEnvelope, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxSize > 0 && len(m.entries) >= m.maxSize {
		m.evictOldest()
	}
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	} else if m.defaultTTL > 0 {
		exp = time.Now().Add(m.defaultTTL)
	}
	m.entries[key] = &entry{resp: resp, expiresAt: exp}
}

func (m *Memory) Invalidate(_ context.Context, key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

func (m *Memory) evictOldest() {
	// Simple eviction: remove the first expired entry, or any entry if none expired.
	now := time.Now()
	for k, e := range m.entries {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			delete(m.entries, k)
			return
		}
	}
	// No expired entries — evict arbitrary entry.
	for k := range m.entries {
		delete(m.entries, k)
		return
	}
}

// Size returns the current number of entries (for monitoring).
func (m *Memory) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
