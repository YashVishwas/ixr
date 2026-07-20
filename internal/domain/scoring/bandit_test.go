package scoring

import (
	"testing"
	"time"

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

// cooldownCandidates isolates the cooldown mechanism from the mean-reward
// comparison: model-a's long track record gives it a much higher mean
// reward than model-b, so only cooldown (not the exploit logic) can
// explain Select avoiding it below.
var cooldownCandidates = []routing.Candidate{
	{Model: "model-a", Score: 0.9},
	{Model: "model-b", Score: 0.5},
}

func TestEpsilonGreedy_CooldownExcludesRepeatedlyFailingArm(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights) // pure exploit
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	for i := 0; i < 20; i++ {
		b.Update("model-a", 1.0) // builds a high mean reward (~1.0)
	}
	b.Update("model-b", 0.5)

	if got := b.Select(cooldownCandidates); got != "model-a" {
		t.Fatalf("before any failures, Select should still favor model-a's higher mean reward, got %q", got)
	}

	// Three consecutive failures (below failureRewardThreshold) trip the
	// cooldown, even though the blended mean reward (~0.87) still beats
	// model-b's 0.5 — proving cooldown, not the mean-reward comparison,
	// is what changes the outcome.
	for i := 0; i < cooldownFailureStreak; i++ {
		b.Update("model-a", 0.0)
	}

	if got := b.Select(cooldownCandidates); got != "model-b" {
		t.Errorf("after %d consecutive failures, Select should avoid the cooling-down model-a despite its higher mean reward, got %q", cooldownFailureStreak, got)
	}
}

func TestEpsilonGreedy_CooldownClearsImmediatelyOnSuccess(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights)
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	for i := 0; i < 20; i++ {
		b.Update("model-a", 1.0)
	}
	b.Update("model-b", 0.5)
	for i := 0; i < cooldownFailureStreak; i++ {
		b.Update("model-a", 0.0)
	}
	if got := b.Select(cooldownCandidates); got != "model-b" {
		t.Fatalf("expected model-a to be cooling down, got %q selected", got)
	}

	b.Update("model-a", 1.0) // a single success should clear the cooldown right away, not gradually

	if got := b.Select(cooldownCandidates); got != "model-a" {
		t.Errorf("a success should clear cooldown immediately so model-a's higher mean reward wins again, got %q", got)
	}
}

func TestEpsilonGreedy_CooldownExpiresAfterBackoff(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights)
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	for i := 0; i < 20; i++ {
		b.Update("model-a", 1.0)
	}
	b.Update("model-b", 0.5)
	for i := 0; i < cooldownFailureStreak; i++ {
		b.Update("model-a", 0.0)
	}
	if got := b.Select(cooldownCandidates); got != "model-b" {
		t.Fatalf("expected model-a to be cooling down, got %q selected", got)
	}

	frozen = frozen.Add(cooldownBase + time.Second) // advance past the base backoff window

	if got := b.Select(cooldownCandidates); got != "model-a" {
		t.Errorf("after the backoff window elapses, model-a should be eligible again, got %q", got)
	}
}

func TestEpsilonGreedy_CooldownBacksOffProgressively(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights)
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	for i := 0; i < 20; i++ {
		b.Update("model-a", 1.0)
	}
	b.Update("model-b", 0.5)
	// One failure past the initial trip: the backoff window should have
	// roughly doubled, not stayed flat.
	for i := 0; i < cooldownFailureStreak+1; i++ {
		b.Update("model-a", 0.0)
	}

	frozen = frozen.Add(cooldownBase + time.Second) // enough to clear the *first* trip's window
	if got := b.Select(cooldownCandidates); got != "model-b" {
		t.Errorf("one extra consecutive failure should extend the cooldown past the base window (progressive backoff), got %q", got)
	}

	frozen = frozen.Add(2 * cooldownBase) // comfortably past the doubled window too
	if got := b.Select(cooldownCandidates); got != "model-a" {
		t.Errorf("cooldown should still eventually expire even after backing off, got %q", got)
	}
}

// TestBandit_CooldownNeverStarvesRoutingWhenAllArmsCoolingDown is the
// adversarial stress case: if the only candidates available are all
// cooling down (e.g. every configured model is currently failing), Select
// must still return something rather than leaving the caller with no
// route at all — cooldown is a preference, not a hard exclusion that can
// wedge routing entirely.
func TestBandit_CooldownNeverStarvesRoutingWhenAllArmsCoolingDown(t *testing.T) {
	b := NewEpsilonGreedy(0.0, DefaultRewardWeights)
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	candidates := []routing.Candidate{{Model: "model-a"}, {Model: "model-b"}}
	for _, c := range candidates {
		for i := 0; i < cooldownFailureStreak; i++ {
			b.Update(c.Model, 0.0)
		}
	}

	if got := b.Select(candidates); got == "" {
		t.Error("Select must return a model even when every candidate is cooling down, not leave routing with nothing")
	}
}

func TestUCB_CooldownExcludesRepeatedlyFailingArm(t *testing.T) {
	b := NewUCB(0.1, DefaultRewardWeights) // low exploration bonus
	frozen := time.Now()
	b.now = func() time.Time { return frozen }

	for i := 0; i < 20; i++ {
		b.Update("model-a", 1.0)
	}
	b.Update("model-b", 0.5)
	for i := 0; i < cooldownFailureStreak; i++ {
		b.Update("model-a", 0.0)
	}

	if got := b.Select(cooldownCandidates); got != "model-b" {
		t.Errorf("UCB should also avoid a cooling-down arm despite its higher score, got %q", got)
	}
}
