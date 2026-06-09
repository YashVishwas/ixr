package scoring

import "testing"

func TestEngineDecideFiltersScoresAndBuildsFallbacks(t *testing.T) {
	decision, ok := (Engine{}).Decide(Request{
		Candidates: []Candidate{
			{Provider: "p1", Model: "cheap-fast"},
			{Provider: "p2", Model: "slow"},
			{Provider: "p3", Model: "open"},
			{Provider: "p4", Model: "fallback"},
		},
		Stats: map[string]ModelSnapshot{
			"cheap-fast": {Cost: 1, P50Latency: 100, P95Latency: 150, SuccessRate: 0.99},
			"slow":       {Cost: 1, P50Latency: 1000, P95Latency: 2000, SuccessRate: 0.99},
			"open":       {Cost: 1, P50Latency: 50, P95Latency: 75, SuccessRate: 0.99, CircuitOpen: true},
			"fallback":   {Cost: 2, P50Latency: 200, P95Latency: 300, SuccessRate: 0.98},
		},
		Weights:       Weights{Cost: 0.3, Latency: 0.5, Reliability: 0.2},
		MaxLatencyP95: 500,
		FallbackLimit: 1,
	})
	if !ok {
		t.Fatal("expected decision")
	}
	if decision.Primary.Model != "cheap-fast" {
		t.Fatalf("primary: got %q, want cheap-fast", decision.Primary.Model)
	}
	if len(decision.Fallbacks) != 1 || decision.Fallbacks[0].Model != "fallback" {
		t.Fatalf("fallbacks: got %+v", decision.Fallbacks)
	}
}

func TestPlanShadowSkipsPrimary(t *testing.T) {
	got := PlanShadow(Candidate{Model: "a"}, ShadowPolicy{Enabled: true, Model: Candidate{Model: "a"}})
	if got.Enabled {
		t.Fatal("shadow should be disabled for primary model")
	}
	got = PlanShadow(Candidate{Model: "a"}, ShadowPolicy{Enabled: true, Model: Candidate{Provider: "p", Model: "b"}})
	if !got.Enabled || got.Model.Model != "b" {
		t.Fatalf("shadow: got %+v", got)
	}
}