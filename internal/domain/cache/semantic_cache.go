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

	// Optional quality tier: a second, independent embedder+backend pair for
	// higher-quality (e.g. real provider-backed) embeddings. Kept fully
	// separate from backend/embedder above rather than sharing storage,
	// because comparing vectors from two different embedders via
	// cosineSimilarity silently scores 0 on dimension mismatch — sharing one
	// backend across two embedding spaces would make every quality-tier
	// entry permanently unmatchable. See WithQualityTier.
	qualityEmbedder Embedder
	qualityBackend  SemanticBackend
	qualityTimeout  time.Duration
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

// WithQualityTier adds an optional second embedder+backend pair used
// alongside (not instead of) the primary fast embedder. It exists to let a
// slow, real embedder (e.g. an OpenAI-backed ProviderEmbedder) catch
// paraphrase-level matches the fast WordVectorizer approximation misses,
// without slowing down or corrupting the existing fast path:
//
//   - Store always writes into the fast tier synchronously (unchanged), then
//     — if a quality tier is configured — embeds and writes into the quality
//     tier asynchronously, off the request path entirely (fire-and-forget,
//     detached from the caller's context so a client disconnect can't cancel
//     it).
//   - Lookup only consults the quality tier when the fast tier misses, and
//     only for up to lookupTimeout — bounding the extra latency a cache miss
//     can incur to something the operator explicitly opted into, rather than
//     silently blowing the fast path's 5ms budget.
//
// backend must be dedicated to this embedder — see the qualityEmbedder field
// doc for why sharing a backend across two embedding spaces is unsafe.
func (s *SemanticCache) WithQualityTier(embedder Embedder, backend SemanticBackend, lookupTimeout time.Duration) *SemanticCache {
	s.qualityEmbedder = embedder
	s.qualityBackend = backend
	s.qualityTimeout = lookupTimeout
	return s
}

func (s *SemanticCache) Lookup(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, CacheHit, bool) {
	if resp, hit, ok := s.exact.Lookup(ctx, req); ok {
		return resp, hit, true
	}

	historyLen, _ := HistoryLenFromContext(ctx)
	text := requestText(req, historyLen)
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

	if resp, ok := s.backend.Find(ctx, vec, s.threshold); ok {
		return resp, CacheHitSemantic, true
	}

	if s.qualityEmbedder == nil || s.qualityBackend == nil {
		return nil, CacheHitNone, false
	}

	// Quality-tier fallback: only reached on a fast-path miss, and bounded by
	// an operator-chosen timeout rather than the fast path's fixed 5ms — this
	// is extra latency the operator explicitly opted into by configuring a
	// quality tier at all, not something imposed on every lookup.
	qCtx, cancel := context.WithTimeout(ctx, s.qualityTimeout)
	defer cancel()

	qVec, err := s.qualityEmbedder.Embed(qCtx, text)
	if err != nil {
		slog.Debug("semantic cache quality-tier embed failed on lookup", "err", err)
		return nil, CacheHitNone, false
	}

	resp, ok := s.qualityBackend.Find(ctx, qVec, s.threshold)
	if !ok {
		return nil, CacheHitNone, false
	}
	return resp, CacheHitSemantic, true
}

func (s *SemanticCache) Store(ctx context.Context, req *schema.RequestEnvelope, resp *schema.ResponseEnvelope, ttl time.Duration) {
	s.exact.Store(ctx, req, resp, ttl)

	historyLen, _ := HistoryLenFromContext(ctx)
	text := requestText(req, historyLen)
	if text == "" {
		return
	}

	vec, err := s.embedder.Embed(ctx, text)
	if err != nil {
		slog.Debug("semantic cache embed failed on store", "err", err)
	} else {
		s.backend.Store(ctx, vec, resp, ttl)
	}

	if s.qualityEmbedder == nil || s.qualityBackend == nil {
		return
	}

	// Detached from ctx: this runs after Store returns, potentially after the
	// triggering request's context has already been cancelled (client
	// disconnect, request completion). Using context.Background() keeps the
	// quality embed from being cancelled by an event unrelated to it.
	qualityEmbedder, qualityBackend := s.qualityEmbedder, s.qualityBackend
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vec, err := qualityEmbedder.Embed(bgCtx, text)
		if err != nil {
			slog.Debug("semantic cache quality-tier embed failed on store", "err", err)
			return
		}
		qualityBackend.Store(bgCtx, vec, resp, ttl)
	}()
}

// requestText extracts user and system message content for embedding,
// starting from historyLen so SessionMiddleware-injected history is
// excluded — only the caller's actual new turn drives semantic matching.
// historyLen 0 (no session middleware, or no history yet) considers the
// full message list, matching pre-fix behavior. Assistant turns are always
// excluded — we match on the input, not the prior exchange.
func requestText(req *schema.RequestEnvelope, historyLen int) string {
	messages := req.Messages
	if historyLen > 0 && historyLen <= len(messages) {
		messages = messages[historyLen:]
	}
	var b strings.Builder
	for _, m := range messages {
		if m.Role == "user" || m.Role == "system" {
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}
