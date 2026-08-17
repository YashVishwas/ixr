package cache

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// vecDim is the fixed dimension of word-hashed vectors.
// 512 gives low collision probability for typical prompts while keeping
// scan cost (512 × n_entries float32 multiplies) negligible.
const vecDim = 512

// WordVectorizer is the built-in Embedder. It maps each token to a bucket
// in a fixed-size float32 vector via FNV-32a hashing, then L2-normalizes.
//
// Similarity scores reflect token overlap, not semantic meaning — sufficient
// for catching near-duplicate prompts with minor wording differences.
// For paraphrase-level matching ("summarize" ≈ "give me a summary"), configure
// a provider-backed Embedder (e.g. Ollama with nomic-embed-text).
//
// Latency: sub-millisecond for typical prompts. Zero external dependencies.
type WordVectorizer struct{}

// Embed returns a normalized word-frequency vector for text.
func (WordVectorizer) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, vecDim)
	for _, tok := range tokenize(text) {
		vec[bucketOf(tok)]++
	}
	l2Normalize(vec)
	return vec, nil
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// ProviderEmbedder adapts a provider.Embedder (e.g. the OpenAI adapter, which
// calls the real /v1/embeddings endpoint) to the cache.Embedder interface.
//
// It produces a different vector space (dimension and values) than
// WordVectorizer, so it must never be compared against WordVectorizer output
// via cosineSimilarity — mismatched dimensions silently score 0 (see
// TestCosineSimilarity_DimMismatch), which would make every entry stored
// through a mismatched pairing permanently unmatchable dead weight. Use it
// with a SemanticBackend that only ever stores and queries vectors produced
// by this same ProviderEmbedder (see SemanticCache.WithQualityTier).
type ProviderEmbedder struct {
	Provider provider.Embedder
	Model    string
}

// Embed calls the wrapped provider's embeddings endpoint for a single input.
func (p ProviderEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.Provider.Embed(ctx, &schema.EmbeddingRequest{Model: p.Model, Input: text})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("cache: provider returned no embedding data")
	}
	return resp.Data[0].Embedding, nil
}

func bucketOf(token string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return h.Sum32() % vecDim
}

func l2Normalize(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return
	}
	inv := 1.0 / math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) * inv)
	}
}
