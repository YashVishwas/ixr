package scoring

import (
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/routing"
)

func TestRewardFormula(t *testing.T) {
	w := RewardWeights{Alpha: 0.3, Beta: 0.2, Gamma: 0.4, Delta: 0.1}
	// normLatency=0, normCost=0, success=true, quality=0
	// = 0.3*(1-0) + 0.2*(1-0) + 0.4*1 + 0.1*0 = 0.9
	got := Reward(0, 0, true, 0, 0, 0, w)
	if got != 0.9 {
		t.Fatalf("reward: got %v, want 0.9", got)
	}
}

func TestEpsilonGreedyExploitsBestMeanReward(t *testing.T) {
	b := NewEpsilonGreedy(0, RewardWeights{})
	b.Update("a", 0.5)
	b.Update("b", 0.9)
	candidates := []routing.Candidate{{Model: "a"}, {Model: "b"}}
	got := b.Select(candidates)
	if got != "b" {
		t.Fatalf("selected: got %q, want b", got)
	}
}

func TestUCBPrefersUntriedArm(t *testing.T) {
	b := NewUCB(2, RewardWeights{})
	b.Update("a", 0.9)
	candidates := []routing.Candidate{{Model: "a"}, {Model: "new"}}
	got := b.Select(candidates)
	if got != "new" {
		t.Fatalf("selected: got %q, want new", got)
	}
}

func TestRegretTracker(t *testing.T) {
	var tracker RegretTracker
	tracker.Record(1.0, 0.75) // regret = 0.25
	tracker.Record(0.5, 0.75) // regret = max(0, 0.5-0.75) = 0
	if tracker.Cumulative() != 0.25 {
		t.Fatalf("cumulative: got %v, want 0.25", tracker.Cumulative())
	}
	if tracker.AverageRegret() != 0.125 {
		t.Fatalf("average: got %v, want 0.125", tracker.AverageRegret())
	}
}
