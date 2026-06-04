package scoring

import (
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/routing"
)

var testCandidates = []routing.Candidate{
	{Model: "model-a", Score: 0.8},
	{Model: "model-b", Score: 0.6},
	{Model: "model-c", Score: 0.4},
}

func TestEpsilonGreedy_SelectsFromCandidates(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights) // ε=0: pure exploit
	model := b.Select(testCandidates)
	// With no prior data and ε=0, should pick candidates[0].Model
	if model == "" {
		t.Fatal("Select should return a non-empty model")
	}
}

func TestEpsilonGreedy_LearnsHighRewardArm(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights) // pure exploit
	// Train: model-b always wins
	for i := 0; i < 20; i++ {
		b.Update("model-a", 0.1)
		b.Update("model-b", 0.9)
		b.Update("model-c", 0.2)
	}
	chosen := b.Select(testCandidates)
	if chosen != "model-b" {
		t.Errorf("after training, exploit should pick model-b (highest reward), got %q", chosen)
	}
}

func TestEpsilonGreedy_RegretTracked(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights)
	b.Update("model-a", 0.5)
	b.Update("model-b", 0.9)
	if b.Regret().Cumulative() < 0 {
		t.Error("cumulative regret must be non-negative")
	}
}

func TestUCB_PrefersUntriedArms(t *testing.T) {
	b := NewUCB(1.0, DefaultRewardWeights)
	// Give model-a a head start
	b.Update("model-a", 0.9)

	// model-b and model-c have no pulls → UCB = +Inf → should be selected
	chosen := b.Select(testCandidates)
	if chosen == "model-a" {
		t.Error("UCB should prefer untried arms over a known-good arm on first pull")
	}
}

func TestUCB_ConvergesOnBestArm(t *testing.T) {
	b := NewUCB(0.1, DefaultRewardWeights) // low exploration
	for i := 0; i < 50; i++ {
		b.Update("model-a", 0.1)
		b.Update("model-b", 0.95)
		b.Update("model-c", 0.3)
	}
	chosen := b.Select(testCandidates)
	if chosen != "model-b" {
		t.Errorf("after many rounds, UCB should converge on model-b, got %q", chosen)
	}
}

func TestUCB_RegretIsNonNegative(t *testing.T) {
	b := NewUCB(1.0, DefaultRewardWeights)
	b.Update("model-a", 0.3)
	b.Update("model-b", 0.7)
	if r := b.Regret().Cumulative(); r < 0 {
		t.Errorf("cumulative regret must be >= 0, got %f", r)
	}
}

func TestRegretTracker_Accumulates(t *testing.T) {
	var rt RegretTracker
	rt.Record(1.0, 0.5) // regret = 0.5
	rt.Record(1.0, 0.8) // regret = 0.2
	want := 0.7
	got := rt.Cumulative()
	if got < want-0.001 || got > want+0.001 {
		t.Errorf("cumulative regret: got %f, want ~%f", got, want)
	}
	avg := rt.AverageRegret()
	if avg < 0.34 || avg > 0.36 {
		t.Errorf("average regret: got %f, want ~0.35", avg)
	}
}

func TestRewardFunction(t *testing.T) {
	w := DefaultRewardWeights
	// perfect latency, perfect cost, success, perfect quality
	r := Reward(0, 0, true, 1.0, 0.0, 0.0, w)
	expected := w.Alpha + w.Beta + w.Gamma + w.Delta
	if r < expected-0.001 || r > expected+0.001 {
		t.Errorf("max reward: got %f, want %f", r, expected)
	}

	// worst case
	rWorst := Reward(0, 0, false, 0.0, 1.0, 1.0, w)
	if rWorst != 0 {
		t.Errorf("worst reward: got %f, want 0", rWorst)
	}
}
