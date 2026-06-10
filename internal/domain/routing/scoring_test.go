package routing

import (
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/pkg/store"
)

func TestFilterAppliesCostCapAndCircuit(t *testing.T) {
	models := []ModelCard{
		{ID: "cheap", InputUSDPer1M: 0.5, OutputUSDPer1M: 0.5},
		{ID: "expensive", InputUSDPer1M: 10, OutputUSDPer1M: 30},
		{ID: "tripped", InputUSDPer1M: 0.5, OutputUSDPer1M: 0.5},
	}
	// One failure with MinRequests=1 trips the circuit open.
	p := circuitbreaker.Policy{
		SuccessRateThreshold: 0.99,
		WindowDuration:       time.Minute,
		MinRequests:          1,
		HalfOpenAfter:        time.Hour,
		ProbeCount:           1,
	}
	cb := circuitbreaker.NewRegistry(p)
	cb.RecordOutcome("tripped", false)

	hint := TaskHint{MaxCostUSDPer1M: 1}
	got := Filter(models, hint, cb)
	if len(got) != 1 || got[0].ID != "cheap" {
		t.Fatalf("filter: got %+v", got)
	}
}

func TestScoreRanksModelByUtility(t *testing.T) {
	cheap := ModelCard{ID: "cheap", InputUSDPer1M: 0.5, OutputUSDPer1M: 0.5, LatencySec: 0.2, FailureRate: 0.01}
	costly := ModelCard{ID: "costly", InputUSDPer1M: 10, OutputUSDPer1M: 30, LatencySec: 2.0, FailureRate: 0.05}
	hint := TaskHint{}
	weights := [3]float64{0.4, 0.4, 0.2}
	inputShare := 0.45
	scoreA := Score(cheap, hint, store.ModelStats{}, weights, inputShare)
	scoreB := Score(costly, hint, store.ModelStats{}, weights, inputShare)
	if scoreA <= scoreB {
		t.Fatalf("cheap (%v) should score higher than costly (%v)", scoreA, scoreB)
	}
}

func TestBuildFallbackChainSkipsSelected(t *testing.T) {
	scored := []Candidate{{Model: "a"}, {Model: "b"}, {Model: "c"}, {Model: "d"}}
	got := BuildFallbackChain(scored, "a", 2)
	if len(got) != 2 || got[0].Model != "b" || got[1].Model != "c" {
		t.Fatalf("fallback chain: got %+v", got)
	}
}
