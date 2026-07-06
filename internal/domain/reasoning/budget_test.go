package reasoning

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestIsReasoningModel(t *testing.T) {
	cases := []struct {
		model    string
		expected bool
	}{
		{"o3", true},
		{"o3-mini", true},
		{"o1", true},
		{"o1-mini", true},
		{"gemini-2.0-flash-thinking", true},
		{"gemini-2.5-pro", true},
		{"deepseek-r1", true},
		{"gpt-4o", false},
		{"claude-opus-4.7", false},
		{"mistral-small-latest", false},
		{"llama-4-scout", false},
	}
	for _, tc := range cases {
		if got := IsReasoningModel(tc.model); got != tc.expected {
			t.Errorf("IsReasoningModel(%q) = %v, want %v", tc.model, got, tc.expected)
		}
	}
}

func TestAdjustTokenBudget_ReasoningModel(t *testing.T) {
	req := &schema.RequestEnvelope{Model: "o3", MaxTokens: 1000}
	adjusted := AdjustTokenBudget(req)

	// o3 overhead=0.90: 1000 / (1 - 0.90) = 10000
	if adjusted.MaxTokens != 10000 {
		t.Fatalf("expected 10000 for o3 with 1000 requested, got %d", adjusted.MaxTokens)
	}
	// Original must not be mutated.
	if req.MaxTokens != 1000 {
		t.Fatal("AdjustTokenBudget must not mutate the original request")
	}
}

func TestAdjustTokenBudget_O3Mini(t *testing.T) {
	req := &schema.RequestEnvelope{Model: "o3-mini", MaxTokens: 500}
	adjusted := AdjustTokenBudget(req)
	// o3-mini overhead=0.80: 500 / 0.20 = 2500
	if adjusted.MaxTokens != 2500 {
		t.Fatalf("expected 2500 for o3-mini with 500 requested, got %d", adjusted.MaxTokens)
	}
}

func TestAdjustTokenBudget_NonReasoningModel(t *testing.T) {
	req := &schema.RequestEnvelope{Model: "gpt-4o", MaxTokens: 1000}
	adjusted := AdjustTokenBudget(req)
	// No adjustment for non-reasoning models — should return same pointer.
	if adjusted != req {
		t.Fatal("non-reasoning model should return req unchanged")
	}
	if adjusted.MaxTokens != 1000 {
		t.Fatalf("expected 1000 unchanged, got %d", adjusted.MaxTokens)
	}
}

func TestAdjustTokenBudget_ZeroMaxTokens(t *testing.T) {
	// MaxTokens=0 means "no limit" — must not adjust.
	req := &schema.RequestEnvelope{Model: "o3", MaxTokens: 0}
	adjusted := AdjustTokenBudget(req)
	if adjusted != req {
		t.Fatal("MaxTokens=0 should return req unchanged")
	}
}

func TestAdjustTokenBudget_Cap(t *testing.T) {
	// Very large requested value should be capped at 100k.
	req := &schema.RequestEnvelope{Model: "o3", MaxTokens: 50000}
	adjusted := AdjustTokenBudget(req)
	// o3: 50000 / 0.10 = 500000 → capped at 100000
	if adjusted.MaxTokens != 100_000 {
		t.Fatalf("expected cap at 100000, got %d", adjusted.MaxTokens)
	}
}

func TestAdjustTokenBudget_LongestPrefixWins(t *testing.T) {
	// o3-mini should match o3-mini (overhead=0.80), not o3 (overhead=0.90).
	req := &schema.RequestEnvelope{Model: "o3-mini", MaxTokens: 1000}
	adjusted := AdjustTokenBudget(req)
	// o3-mini: 1000 / 0.20 = 5000
	if adjusted.MaxTokens != 5000 {
		t.Fatalf("expected 5000 (o3-mini overhead), got %d — o3 prefix may have matched instead", adjusted.MaxTokens)
	}
}

func TestAdjustTokenBudget_DoesNotMutate(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:     "o1",
		MaxTokens: 2000,
		Messages:  []schema.Message{{Role: "user", Content: "hello"}},
	}
	_ = AdjustTokenBudget(req)
	if req.MaxTokens != 2000 {
		t.Fatal("original request was mutated")
	}
}
