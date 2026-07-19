package cost

import "testing"

func TestForUsage_KnownModel(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", 1_000_000, 1_000_000)
	if got.InputUSD != 3 || got.OutputUSD != 15 || got.TotalUSD != 18 {
		t.Fatalf("got %+v, want input=3 output=15 total=18", got)
	}
}

func TestForUsage_UnknownModel(t *testing.T) {
	got := ForUsage("some-model-not-in-catalog", 1000, 1000)
	if got.TotalUSD != 0 {
		t.Fatalf("expected zero cost for unpriced model, got %+v", got)
	}
}

func TestForUsage_ZeroTokens(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", 0, 0)
	if got.TotalUSD != 0 {
		t.Fatalf("expected zero cost for zero tokens, got %+v", got)
	}
}
