package cache

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
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
