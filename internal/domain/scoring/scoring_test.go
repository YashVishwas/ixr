package scoring

import (
	"math/rand"
	"testing"
)

func TestRewardClampsInputs(t *testing.T) {
	got := Reward(RewardWeights{Latency: 1, Cost: 1, Reliability: 1, Quality: 1}, RewardInput{
		LatencyMS:    10,
		CostPerToken: 2,
		SuccessRate:  2,
		QualityScore: -1,
	})
	if got != 1.1 {
		t.Fatalf("reward: got %v, want 1.1", got)
	}
}

func TestEpsilonGreedyExploitsBestMeanReward(t *testing.T) {
	got := EpsilonGreedy([]ArmStats{
		{Model: "a", Pulls: 10, TotalReward: 5},
		{Model: "b", Pulls: 10, TotalReward: 9},
	}, 0, rand.New(rand.NewSource(1)))
	if got != "b" {
		t.Fatalf("selected: got %q, want b", got)
	}
}

func TestUCBPrefersUntriedArm(t *testing.T) {
	got := UCB([]ArmStats{
		{Model: "a", Pulls: 10, TotalReward: 9},
		{Model: "new"},
	}, 2)
	if got != "new" {
		t.Fatalf("selected: got %q, want new", got)
	}
}

func TestRegretTracker(t *testing.T) {
	var tracker RegretTracker
	tracker.Observe(1.0, 0.75)
	tracker.Observe(0.5, 0.75)
	if tracker.Cumulative != 0.25 || tracker.Average() != 0.125 {
		t.Fatalf("tracker: got %+v avg=%v", tracker, tracker.Average())
	}
}