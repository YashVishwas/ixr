package cache

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Embedder converts text into a dense float32 vector.
// The built-in implementation is WordVectorizer — pure Go, zero deps, sub-millisecond.
// Swap in a provider-backed embedder for higher-quality paraphrase matching.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// SemanticBackend stores and retrieves cached responses by vector similarity.
type SemanticBackend interface {
	// Find returns the best-matching cached response whose cosine similarity to vec
	// is >= threshold. Returns false when the store is empty or no match qualifies.
	Find(ctx context.Context, vec []float32, threshold float32) (*schema.ResponseEnvelope, bool)
	// Store saves resp alongside its embedding vector.
	Store(ctx context.Context, vec []float32, resp *schema.ResponseEnvelope, ttl time.Duration)
}

// vectorEntry holds a cached response with its embedding and expiry.
type vectorEntry struct {
	vec       []float32
	resp      *schema.ResponseEnvelope
	expiresAt time.Time
}

// MemorySemanticBackend is an in-process cosine-similarity store.
// Lookup is O(n) over stored entries — appropriate for caches up to ~10k entries
// where each scan takes well under 1ms on modern hardware.
// No external dependencies; safe for concurrent use.
type MemorySemanticBackend struct {
	mu      sync.Mutex
	entries []*vectorEntry
	maxSize int
}

// NewMemorySemanticBackend creates an in-memory semantic backend.
// maxSize=0 means unlimited.
func NewMemorySemanticBackend(maxSize int) *MemorySemanticBackend {
	return &MemorySemanticBackend{maxSize: maxSize}
}

// Find returns the highest-similarity cached response that meets threshold.
func (b *MemorySemanticBackend) Find(_ context.Context, vec []float32, threshold float32) (*schema.ResponseEnvelope, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()

	var best *schema.ResponseEnvelope
	var bestScore float32

	for _, e := range b.entries {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			continue
		}
		if score := cosineSimilarity(vec, e.vec); score >= threshold && score > bestScore {
			bestScore = score
			best = e.resp
		}
	}

	if best != nil {
		return best, true
	}
	return nil, false
}

// Store saves a response with its embedding. Evicts the oldest entry when full.
func (b *MemorySemanticBackend) Store(_ context.Context, vec []float32, resp *schema.ResponseEnvelope, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.maxSize > 0 && len(b.entries) >= b.maxSize {
		b.evict()
	}

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	b.entries = append(b.entries, &vectorEntry{vec: vec, resp: resp, expiresAt: exp})
}

// Len returns the number of stored entries (for monitoring).
func (b *MemorySemanticBackend) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

func (b *MemorySemanticBackend) evict() {
	now := time.Now()
	for i, e := range b.entries {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			b.entries = append(b.entries[:i], b.entries[i+1:]...)
			return
		}
	}
	// No expired entries — remove the oldest (front of slice).
	if len(b.entries) > 0 {
		b.entries = b.entries[1:]
	}
}

// cosineSimilarity returns the cosine similarity in [0,1] between a and b.
// Returns 0 for mismatched dimensions or zero-magnitude vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// journalEntry is a single line in the on-disk semantic cache journal.
type journalEntry struct {
	Vec       []float32                `json:"vec"`
	Resp      *schema.ResponseEnvelope `json:"resp"`
	ExpiresAt time.Time                `json:"expires_at,omitempty"`
}

// PersistentSemanticBackend wraps MemorySemanticBackend with an append-only
// JSON-lines journal. On startup it replays non-expired entries from disk into
// memory. No external service is required — mount a volume at the journal
// directory for cross-restart durability.
//
// If dir is empty or the directory is not writable, it silently degrades to
// pure in-memory operation so a missing mount never breaks the request path.
type PersistentSemanticBackend struct {
	*MemorySemanticBackend
	mu   sync.Mutex
	file *os.File
}

// NewPersistentSemanticBackend loads dir/semantic.jsonl into memory then opens
// it for appending. dir="" returns a plain in-memory backend.
func NewPersistentSemanticBackend(maxSize int, dir string) *PersistentSemanticBackend {
	b := &PersistentSemanticBackend{MemorySemanticBackend: NewMemorySemanticBackend(maxSize)}
	if dir == "" {
		return b
	}
	path := filepath.Join(dir, "semantic.jsonl")
	b.replayJournal(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("semantic cache: journal unavailable, running in-memory only", "path", path, "err", err)
		return b
	}
	b.file = f
	return b
}

func (b *PersistentSemanticBackend) replayJournal(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // first run — file doesn't exist yet
	}
	defer f.Close()

	now := time.Now()
	var loaded int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<20), 4<<20) // 4 MB max per line
	for scanner.Scan() {
		var e journalEntry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
			continue
		}
		var ttl time.Duration
		if !e.ExpiresAt.IsZero() {
			ttl = time.Until(e.ExpiresAt)
		}
		b.MemorySemanticBackend.Store(context.Background(), e.Vec, e.Resp, ttl)
		loaded++
	}
	if loaded > 0 {
		slog.Info("semantic cache: replayed journal", "entries", loaded, "path", path)
	}
}

// Store writes to memory and appends to the journal file.
func (b *PersistentSemanticBackend) Store(ctx context.Context, vec []float32, resp *schema.ResponseEnvelope, ttl time.Duration) {
	b.MemorySemanticBackend.Store(ctx, vec, resp, ttl)
	if b.file == nil {
		return
	}
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	line, err := json.Marshal(journalEntry{Vec: vec, Resp: resp, ExpiresAt: exp})
	if err != nil {
		return
	}
	b.mu.Lock()
	_, _ = b.file.Write(append(line, '\n'))
	b.mu.Unlock()
}

// Close flushes and closes the journal file. Call on server shutdown.
func (b *PersistentSemanticBackend) Close() error {
	if b.file != nil {
		return b.file.Close()
	}
	return nil
}
