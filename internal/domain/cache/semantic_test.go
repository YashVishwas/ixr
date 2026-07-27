package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
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

// sharedHistory is a long, topically consistent SessionMiddleware-style
// injected history: several user/assistant pairs about the French
// Revolution. It's long enough relative to a short new turn that it
// dominates a naive full-message-list token-overlap score.
func sharedHistory() []schema.Message {
	return []schema.Message{
		{Role: "user", Content: "Can you give me an overview of the French Revolution, its main causes, and why it started when it did"},
		{Role: "assistant", Content: "The French Revolution began in 1789, driven by financial crisis, food shortages, resentment of absolute monarchy, and Enlightenment ideas about liberty and equality"},
		{Role: "user", Content: "Who were the key figures involved in the French Revolution and what roles did they play in its events"},
		{Role: "assistant", Content: "Key figures included Robespierre, Danton, Marat, and Louis XVI, each playing distinct roles across the Revolution's escalating radical phases"},
		{Role: "user", Content: "What were the long term consequences of the French Revolution for France and for Europe as a whole"},
		{Role: "assistant", Content: "The Revolution reshaped France's political system, ended feudal privilege, and inspired nationalist and republican movements across Europe for decades afterward"},
	}
}

// TestSemanticCache_SessionHistoryCausesFalseHitWithoutHistoryLen
// reproduces the bug documented as unresolved in
// docs/rfc/0001-semantic-cache.md Open Question #10: when the caller's
// context has no historyLen (the state before SessionMiddleware wires it
// through), a rich shared history dominates the token-overlap score enough
// that two completely unrelated new questions in the same session score as
// a semantic hit against each other.
func TestSemanticCache_SessionHistoryCausesFalseHitWithoutHistoryLen(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.92)
	ctx := context.Background() // no historyLen set — pre-fix behavior

	history := sharedHistory()
	stored := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "What year did the revolution start?"})}
	sc.Store(ctx, stored, makeResp("revolution-answer"), time.Minute)

	// Same injected history, but a genuinely unrelated new question.
	query := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "Give me a recipe for chocolate cake."})}
	_, hit, ok := sc.Lookup(ctx, query)

	if !ok || hit != CacheHitSemantic {
		t.Fatalf("expected this to reproduce the documented false hit (shared history dominating token overlap) when no historyLen is set, got hit=%v ok=%v — if this no longer reproduces, the scoring changed and TestSemanticCache_HistoryLenPreventsSessionFalseHit's improvement can no longer be trusted to mean what it claims", hit, ok)
	}
}

// TestSemanticCache_HistoryLenPreventsSessionFalseHit is the fix: once
// SessionMiddleware communicates historyLen via cache.WithHistoryLen, the
// exact same two requests as above (identical shared history, unrelated
// new questions) correctly miss instead of false-hitting, because only the
// net-new turn drives the embedding.
func TestSemanticCache_HistoryLenPreventsSessionFalseHit(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.92)

	history := sharedHistory()
	historyLen := len(history)
	ctx := WithHistoryLen(context.Background(), historyLen)

	stored := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "What year did the revolution start?"})}
	sc.Store(ctx, stored, makeResp("revolution-answer"), time.Minute)

	query := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "Give me a recipe for chocolate cake."})}
	_, hit, ok := sc.Lookup(ctx, query)

	if ok {
		t.Errorf("expected a miss once history is excluded from the embedding — the two new turns share no topic, got hit=%v", hit)
	}
}

// TestSemanticCache_HistoryLenStillMatchesGenuineNearDuplicateNewTurn
// guards against the fix being too aggressive: a genuinely near-duplicate
// new turn (same shared history, near-identical new question) should still
// hit once history is excluded — the fix scopes the embedding to the new
// turn, it doesn't disable semantic matching on that turn.
func TestSemanticCache_HistoryLenStillMatchesGenuineNearDuplicateNewTurn(t *testing.T) {
	mem := NewMemory(100, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(100)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.88)

	history := sharedHistory()
	historyLen := len(history)
	ctx := WithHistoryLen(context.Background(), historyLen)

	stored := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "please summarize this document for me"})}
	sc.Store(ctx, stored, makeResp("sem-hit"), time.Minute)

	query := &schema.RequestEnvelope{Model: "gpt-4o", Messages: append(append([]schema.Message(nil), history...),
		schema.Message{Role: "user", Content: "please summarize this document for me quickly"})}
	_, hit, ok := sc.Lookup(ctx, query)

	if !ok || hit != CacheHitSemantic {
		t.Errorf("expected a semantic hit on a genuine near-duplicate new turn even with history excluded, got hit=%v ok=%v", hit, ok)
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

// TestSemanticCache_ConcurrentLookupAndStore stresses the cache the way
// production traffic actually would — many goroutines hitting Lookup and
// Store simultaneously with a mix of overlapping and distinct requests —
// rather than the sequential single-goroutine pattern every other test in
// this file uses. Run under go test -race; the only property under test
// is "no data race and no panic," not any particular hit/miss outcome.
func TestSemanticCache_ConcurrentLookupAndStore(t *testing.T) {
	mem := NewMemory(1000, time.Minute)
	exact := &ExactCache{mem}
	backend := NewMemorySemanticBackend(1000)
	sc := NewSemanticCache(exact, backend, WordVectorizer{}, 0.92)
	ctx := context.Background()

	const goroutines = 50
	const opsPerGoroutine = 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				topic := (g + i) % 5 // overlapping topics across goroutines, so Lookup sometimes hits
				req := makeReq("gpt-4o", "question about topic number "+string(rune('A'+topic)))
				if _, _, ok := sc.Lookup(ctx, req); !ok {
					sc.Store(ctx, req, makeResp("resp"), time.Minute)
				}
			}
		}(g)
	}
	wg.Wait()
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
