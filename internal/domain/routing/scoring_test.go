package routing

import "testing"

func TestFilterCandidatesAppliesConstraintsAndCircuit(t *testing.T) {
	candidates := []Candidate{
		{Provider: "p", Model: "cheap"},
		{Provider: "p", Model: "slow"},
		{Provider: "p", Model: "open"},
	}
	stats := map[string]ModelStats{
		"cheap": {CostUSDPer1M: 0.5, P95LatencyMS: 100, SuccessRate: 0.99},
		"slow":  {CostUSDPer1M: 0.4, P95LatencyMS: 2000, SuccessRate: 0.99},
		"open":  {CostUSDPer1M: 0.4, P95LatencyMS: 100, CircuitOpen: true},
	}
	got := FilterCandidates(candidates, stats, Constraints{MaxCostUSDPer1M: 1, MaxLatencyMS: 500})
	if len(got) != 1 || got[0].Model != "cheap" {
		t.Fatalf("filtered: got %+v", got)
	}
}

func TestScoreCandidatesSortsLowestScoreFirst(t *testing.T) {
	candidates := []Candidate{{Model: "a"}, {Model: "b"}}
	stats := map[string]ModelStats{
		"a": {CostUSDPer1M: 1, P50LatencyMS: 100, SuccessRate: 0.99},
		"b": {CostUSDPer1M: 5, P50LatencyMS: 500, SuccessRate: 0.90},
	}
	got := ScoreCandidates(candidates, stats, Weights{Cost: 0.4, Latency: 0.4, Reliability: 0.2})
	if got[0].Model != "a" {
		t.Fatalf("best: got %q, want a; all=%+v", got[0].Model, got)
	}
}

func TestBuildFallbackChainSkipsSelected(t *testing.T) {
	scored := []Candidate{{Model: "a"}, {Model: "b"}, {Model: "c"}, {Model: "d"}}
	got := BuildFallbackChain(scored, "a", 2)
	if len(got) != 2 || got[0].Model != "b" || got[1].Model != "c" {
		t.Fatalf("fallback chain: got %+v", got)
	}
}