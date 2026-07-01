package routing

import (
	"fmt"
	"testing"
)

// --- IsContextLengthError ---

func TestIsContextLengthError_OpenAI(t *testing.T) {
	err := fmt.Errorf("openai: status 400: {\"error\":{\"code\":\"context_length_exceeded\"}}")
	if !IsContextLengthError(err) {
		t.Fatal("expected OpenAI context_length_exceeded to be detected")
	}
}

func TestIsContextLengthError_Anthropic(t *testing.T) {
	err := fmt.Errorf("anthropic: status 400: prompt is too long for the model")
	if !IsContextLengthError(err) {
		t.Fatal("expected Anthropic context length error to be detected")
	}
}

func TestIsContextLengthError_Generic(t *testing.T) {
	err := fmt.Errorf("provider: too many tokens in request")
	if !IsContextLengthError(err) {
		t.Fatal("expected generic token error to be detected")
	}
}

func TestIsContextLengthError_ServerError(t *testing.T) {
	err := fmt.Errorf("openai: status 500: internal server error")
	if IsContextLengthError(err) {
		t.Fatal("500 server error should not be treated as context length error")
	}
}

func TestIsContextLengthError_RateLimit(t *testing.T) {
	err := fmt.Errorf("openai: status 429: rate limit exceeded")
	if IsContextLengthError(err) {
		t.Fatal("rate limit error should not be treated as context length error")
	}
}

func TestIsContextLengthError_Nil(t *testing.T) {
	if IsContextLengthError(nil) {
		t.Fatal("nil should not be a context length error")
	}
}

// --- ContextWindowFor ---

func TestContextWindowFor_CatalogModel(t *testing.T) {
	if w := ContextWindowFor("claude-opus-4.7"); w != 200_000 {
		t.Fatalf("expected 200000 for claude-opus-4.7, got %d", w)
	}
}

func TestContextWindowFor_LlamaScout(t *testing.T) {
	if w := ContextWindowFor("llama-4-scout"); w != 10_000_000 {
		t.Fatalf("expected 10000000 for llama-4-scout, got %d", w)
	}
}

func TestContextWindowFor_KnownModel(t *testing.T) {
	if w := ContextWindowFor("gpt-4o"); w != 128_000 {
		t.Fatalf("expected 128000 for gpt-4o, got %d", w)
	}
}

func TestContextWindowFor_UnknownModel(t *testing.T) {
	if w := ContextWindowFor("some-unknown-model-v999"); w != defaultContextWindow {
		t.Fatalf("expected default %d for unknown model, got %d", defaultContextWindow, w)
	}
}

// --- escalateCandidates ---

func TestEscalateCandidates_FiltersSmaller(t *testing.T) {
	remaining := []Candidate{
		{Model: "gpt-4"},            // 8k — below threshold
		{Model: "gpt-4o"},           // 128k — equal, not greater
		{Model: "claude-opus-4.7"},  // 200k — eligible
	}
	eligible := escalateCandidates(remaining, 128_000)
	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible candidate, got %d: %v", len(eligible), eligible)
	}
	if eligible[0].Model != "claude-opus-4.7" {
		t.Fatalf("expected claude-opus-4.7, got %s", eligible[0].Model)
	}
}

func TestEscalateCandidates_SortedSmallestFirst(t *testing.T) {
	// gemini-3.1-pro (1M) and claude-opus-4.7 (200k) both exceed 128k.
	// claude-opus-4.7 should come first (smaller sufficient window = cheaper escalation).
	remaining := []Candidate{
		{Model: "gemini-3.1-pro"},
		{Model: "claude-opus-4.7"},
	}
	eligible := escalateCandidates(remaining, 128_000)
	if len(eligible) != 2 {
		t.Fatalf("expected 2 eligible, got %d", len(eligible))
	}
	if eligible[0].Model != "claude-opus-4.7" {
		t.Fatalf("expected claude-opus-4.7 first (200k < 1M), got %s", eligible[0].Model)
	}
}

func TestEscalateCandidates_EmptyWhenNoneEligible(t *testing.T) {
	remaining := []Candidate{
		{Model: "gpt-4"},    // 8k
		{Model: "gpt-4o"},  // 128k — equal to threshold, not greater
	}
	eligible := escalateCandidates(remaining, 128_000)
	if len(eligible) != 0 {
		t.Fatalf("expected 0 eligible candidates, got %d", len(eligible))
	}
}

func TestEscalateCandidates_EmptyInput(t *testing.T) {
	eligible := escalateCandidates(nil, 128_000)
	if len(eligible) != 0 {
		t.Fatal("nil input should return empty slice")
	}
}
