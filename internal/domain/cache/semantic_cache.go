package cache

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// CacheHit indicates which cache layer produced a hit.
type CacheHit uint8

const (
	CacheHitNone     CacheHit = iota
	CacheHitExact             // SHA-256 exact match
	CacheHitSemantic          // cosine-similarity match
)

func (h CacheHit) String() string {
	switch h {
	case CacheHitExact:
		return "EXACT-HIT"
	case CacheHitSemantic:
		return "SEMANTIC-HIT"
	default:
		return "MISS"
	}
}

// RequestAwareCache is the interface used by CacheMiddleware.
// Implementations receive the full RequestEnvelope — not just a hash — so
// semantic backends can embed the original prompt text.
type RequestAwareCache interface {
	Lookup(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, CacheHit, bool)
	Store(ctx context.Context, req *schema.RequestEnvelope, resp *schema.ResponseEnvelope, ttl time.Duration)
}

// ExactCache wraps Memory as a RequestAwareCache using SHA-256 keying.
// It is the default when no semantic backend is configured.
type ExactCache struct{ *Memory }

func (e *ExactCache) Lookup(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, CacheHit, bool) {
	resp, ok := e.Get(ctx, Key(req))
	if !ok {
		return nil, CacheHitNone, false
	}
	return resp, CacheHitExact, true
}

func (e *ExactCache) Store(ctx context.Context, req *schema.RequestEnvelope, resp *schema.ResponseEnvelope, ttl time.Duration) {
	e.Set(ctx, Key(req), resp, ttl)
}

// SemanticCache is a two-layer cache: exact-match first, semantic fallback second.
//
// On Lookup:
//  1. Exact hash match (O(1), free).
//  2. Embed the prompt with the configured Embedder.
//  3. Cosine-similarity scan over stored vectors.
//
// On Store:
//  1. Write to exact-match store immediately.
//  2. Embed and write to semantic backend.
//
// Store is called after the response has already been flushed to the caller,
// so the embedding step never adds latency to the request path.
type SemanticCache struct {
	exact     *ExactCache
	backend   SemanticBackend
	embedder  Embedder
	threshold float32
}

// NewSemanticCache creates a two-layer cache.
// threshold is the minimum cosine similarity for a semantic hit (0–1).
// 0.92 is a good default with the built-in WordVectorizer.
func NewSemanticCache(exact *ExactCache, backend SemanticBackend, embedder Embedder, threshold float32) *SemanticCache {
	return &SemanticCache{
		exact:     exact,
		backend:   backend,
		embedder:  embedder,
		threshold: threshold,
	}
}

func (s *SemanticCache) Lookup(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, CacheHit, bool) {
	if resp, hit, ok := s.exact.Lookup(ctx, req); ok {
		return resp, hit, true
	}

	text := requestText(req)
	if text == "" {
		return nil, CacheHitNone, false
	}

	// Hard cap so a slow or remote embedder never adds latency to the request path.
	// WordVectorizer is sub-ms; this only protects against misconfigured embedders.
	embedCtx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()

	vec, err := s.embedder.Embed(embedCtx, text)
	if err != nil {
		slog.Debug("semantic cache embed failed on lookup", "err", err)
		return nil, CacheHitNone, false
	}

	resp, ok := s.backend.Find(ctx, vec, s.threshold)
	if !ok {
		return nil, CacheHitNone, false
	}
	return resp, CacheHitSemantic, true
}

func (s *SemanticCache) Store(ctx context.Context, req *schema.RequestEnvelope, resp *schema.ResponseEnvelope, ttl time.Duration) {
	s.exact.Store(ctx, req, resp, ttl)

	text := requestText(req)
	if text == "" {
		return
	}

	vec, err := s.embedder.Embed(ctx, text)
	if err != nil {
		slog.Debug("semantic cache embed failed on store", "err", err)
		return
	}

	s.backend.Store(ctx, vec, resp, ttl)
}

// requestText extracts user and system message content for embedding.
// Assistant turns are excluded — we match on the input, not the prior exchange.
func requestText(req *schema.RequestEnvelope) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "system" {
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}
