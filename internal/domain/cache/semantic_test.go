package cache

import (
	"context"
	"testing"
	"time"
)

// --- WordVectorizer ---

func TestWordVectorizer_Deterministic(t *testing.T) {
	v := WordVectorizer{}
	a, _ := v.Embed(context.Background(), "summarize this document")
	b, _ := v.Embed(context.Background(), "summarize this document")
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("WordVectorizer is not deterministic")
		}
	}
}

func TestWordVectorizer_L2Normalized(t *testing.T) {
	v := WordVectorizer{}
	vec, _ := v.Embed(context.Background(), "hello world foo bar")
	var norm float32
	for _, x := range vec {
		norm += x * x
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("vector not unit-normalized: norm=%f", norm)
	}
}

func TestWordVectorizer_EmptyText(t *testing.T) {
	v := WordVectorizer{}
	vec, err := v.Embed(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var norm float32
	for _, x := range vec {
		norm += x * x
	}
	if norm != 0 {
		t.Fatal("empty text should produce zero vector")
	}
}

func TestWordVectorizer_CorrectDimension(t *testing.T) {
	v := WordVectorizer{}
	vec, _ := v.Embed(context.Background(), "some prompt text here")
	if len(vec) != vecDim {
		t.Fatalf("expected dim=%d, got %d", vecDim, len(vec))
	}
}

// --- cosineSimilarity ---

func TestCosineSimilarity_Identical(t *testing.T) {
	v := WordVectorizer{}
	vec, _ := v.Embed(context.Background(), "what is the capital of France")
	score := cosineSimilarity(vec, vec)
	if score < 0.999 {
		t.Fatalf("identical vectors: expected ~1.0, got %f", score)
	}
}

func TestCosineSimilarity_Empty(t *testing.T) {
	score := cosineSimilarity([]float32{}, []float32{})
	if score != 0 {
		t.Fatalf("empty vectors should return 0, got %f", score)
	}
}

func TestCosineSimilarity_DimMismatch(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0, 0}
	if cosineSimilarity(a, b) != 0 {
		t.Fatal("mismatched dims should return 0")
	}
}

func TestCosineSimilarity_SimilarTexts(t *testing.T) {
	v := WordVectorizer{}
	// Near-duplicate: one extra word
	a, _ := v.Embed(context.Background(), "please summarize this document")
	b, _ := v.Embed(context.Background(), "please summarize this document clearly")
	score := cosineSimilarity(a, b)
	if score < 0.85 {
		t.Fatalf("near-duplicate texts scored too low: %f", score)
	}
}

func TestCosineSimilarity_UnrelatedTexts(t *testing.T) {
	v := WordVectorizer{}
	a, _ := v.Embed(context.Background(), "summarize the quarterly earnings report")
	b, _ := v.Embed(context.Background(), "write a poem about autumn leaves")
	score := cosineSimilarity(a, b)
	if score > 0.5 {
		t.Fatalf("unrelated texts scored too high: %f", score)
	}
}

// --- MemorySemanticBackend ---

func TestMemorySemanticBackend_HitAndMiss(t *testing.T) {
	backend := NewMemorySemanticBackend(100)
	v := WordVectorizer{}
	ctx := context.Background()

	vec, _ := v.Embed(ctx, "what is the weather today")
	resp := makeResp("r1")
	backend.Store(ctx, vec, resp, time.Minute)

	// Exact same vector — must hit.
	got, ok := backend.Find(ctx, vec, 0.99)
	if !ok {
		t.Fatal("expected hit on stored vector")
	}
	if got.ID != "r1" {
		t.Fatalf("got ID=%q, want r1", got.ID)
	}
}

func TestMemorySemanticBackend_BelowThreshold(t *testing.T) {
	backend := NewMemorySemanticBackend(100)
	v := WordVectorizer{}
	ctx := context.Background()

	storeVec, _ := v.Embed(ctx, "summarize the quarterly earnings report")
	backend.Store(ctx, storeVec, makeResp("r1"), time.Minute)

	queryVec, _ := v.Embed(ctx, "write a poem about autumn leaves")
	_, ok := backend.Find(ctx, queryVec, 0.92)
	if ok {
		t.Fatal("dissimilar query should not hit above threshold 0.92")
	}
}

func TestMemorySemanticBackend_TTLExpiry(t *testing.T) {
	backend := NewMemorySemanticBackend(100)
	v := WordVectorizer{}
	ctx := context.Background()

	vec, _ := v.Embed(ctx, "hello world")
	backend.Store(ctx, vec, makeResp("r1"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, ok := backend.Find(ctx, vec, 0.99)
	if ok {
		t.Fatal("TTL-expired entry should not be returned")
	}
}

func TestMemorySemanticBackend_Eviction(t *testing.T) {
	backend := NewMemorySemanticBackend(3)
	v := WordVectorizer{}
	ctx := context.Background()

	for i := range 5 {
		vec, _ := v.Embed(ctx, string(rune('a'+i))+" test prompt")
		backend.Store(ctx, vec, makeResp("r"), time.Minute)
	}
	if backend.Len() > 3 {
		t.Fatalf("backend exceeded maxSize: len=%d", backend.Len())
	}
}

// --- SemanticCache ---

func TestSemanticCache_ExactHitFirst(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.92)
	ctx := context.Background()

	req := makeReq("gpt-4o", "hello exact")
	resp := makeResp("exact-hit")
	sc.Store(ctx, req, resp, time.Minute)

	got, hit, ok := sc.Lookup(ctx, req)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.ID != "exact-hit" {
		t.Fatalf("got ID=%q, want exact-hit", got.ID)
	}
	if hit != CacheHitExact {
		t.Fatalf("expected CacheHitExact, got %v", hit)
	}
}

func TestSemanticCache_SemanticFallback(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	// Lower threshold so near-duplicates hit with the word vectorizer.
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.88)
	ctx := context.Background()

	stored := makeReq("gpt-4o", "please summarize this document for me")
	sc.Store(ctx, stored, makeResp("sem-hit"), time.Minute)

	// Near-duplicate with one extra word — exact miss, semantic hit.
	query := makeReq("gpt-4o", "please summarize this document for me quickly")
	got, hit, ok := sc.Lookup(ctx, query)
	if !ok {
		t.Fatal("expected semantic cache hit on near-duplicate")
	}
	if got.ID != "sem-hit" {
		t.Fatalf("got ID=%q, want sem-hit", got.ID)
	}
	if hit != CacheHitSemantic {
		t.Fatalf("expected CacheHitSemantic, got %v", hit)
	}
}

func TestSemanticCache_Miss(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.92)
	ctx := context.Background()

	sc.Store(ctx, makeReq("gpt-4o", "summarize quarterly earnings"), makeResp("r1"), time.Minute)

	_, hit, ok := sc.Lookup(ctx, makeReq("gpt-4o", "write a haiku about mountains"))
	if ok {
		t.Fatal("unrelated query should miss")
	}
	if hit != CacheHitNone {
		t.Fatalf("expected CacheHitNone on miss, got %v", hit)
	}
}

func TestExactCache_RoundTrip(t *testing.T) {
	exact := &ExactCache{NewMemory(100, time.Minute)}
	ctx := context.Background()

	req := makeReq("claude-3-5-sonnet", "test prompt")
	resp := makeResp("ec-1")
	exact.Store(ctx, req, resp, time.Minute)

	got, hit, ok := exact.Lookup(ctx, req)
	if !ok {
		t.Fatal("expected hit")
	}
	if got.ID != "ec-1" {
		t.Fatalf("got ID=%q, want ec-1", got.ID)
	}
	if hit != CacheHitExact {
		t.Fatalf("expected CacheHitExact, got %v", hit)
	}
}

func TestCacheHit_String(t *testing.T) {
	if CacheHitExact.String() != "EXACT-HIT" {
		t.Fatalf("unexpected: %s", CacheHitExact.String())
	}
	if CacheHitSemantic.String() != "SEMANTIC-HIT" {
		t.Fatalf("unexpected: %s", CacheHitSemantic.String())
	}
	if CacheHitNone.String() != "MISS" {
		t.Fatalf("unexpected: %s", CacheHitNone.String())
	}
}
