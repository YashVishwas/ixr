package routing

import (
	"testing"
)

func TestRoute_ReasoningTaskFrontier(t *testing.T) {
	hint := TaskHint{
		PromptChars:     8000,
		ReasoningScore:  1.0,
		MathScore:       0.9,
		CodingScore:     0.1,
		MaxCostUSDPer1M: 100,
	}
	got := Route(hint)
	if got != "claude-opus-4.7" && got != "gpt-5.2" {
		t.Fatalf("expected frontier reasoning model, got %q", got)
	}
}

func TestRoute_BudgetCapFiltersExpensive(t *testing.T) {
	hint := TaskHint{
		ReasoningScore:  0.5,
		MaxCostUSDPer1M: 0.10,
	}
	got := Route(hint)
	if got != "gemma-3-27b" {
		t.Fatalf("expected cheapest model under tight budget, got %q", got)
	}
}

func TestRoute_LatencySensitiveAvoidsSlowModels(t *testing.T) {
	hint := TaskHint{
		PromptChars:      2000,
		CodingScore:      1.0,
		ReasoningScore:   0.4,
		LatencySensitive: true,
		MaxCostUSDPer1M:  50,
	}
	got := Route(hint)
	if got != "gpt-5.3-codex" {
		t.Fatalf("expected fast coding model, got %q", got)
	}
}

func TestRoute_ZeroHintSensibleDefault(t *testing.T) {
	got := Route(TaskHint{})
	if got == "" {
		t.Fatal("expected non-empty default route")
	}
	allowed := map[string]struct{}{
		"gemma-3-27b":      {},
		"gpt-5.3-codex":    {},
		"llama-4-scout":    {},
		"deepseek-v3-0324": {},
		"gpt-5.2":          {},
	}
	if _, ok := allowed[got]; !ok {
		t.Fatalf("unexpected zero-hint pick %q (want a cheap / low-latency default)", got)
	}
}

func TestRoute_NoModelUnderBudget(t *testing.T) {
	hint := TaskHint{
		MaxCostUSDPer1M: 0.001,
	}
	if got := Route(hint); got != "" {
		t.Fatalf("expected empty when no model fits budget, got %q", got)
	}
}
