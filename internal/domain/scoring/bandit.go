package scoring

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/routing"
)

// Bandit is the interface for adaptive model-selection algorithms.
type Bandit interface {
	Select(candidates []routing.Candidate) string
	Update(model string, reward float64)
	Regret() *RegretTracker
}

// failureRewardThreshold classifies a reward as a failed call for cooldown
// purposes. It is calibrated to the only reward source actually wired to
// Update today (shadow.go's orchestrator and plugins/banditreward): both
// compute reward as success*0.7 + fast(<2s)*0.3, so every success scores
// >= 0.7 and every failure scores <= 0.3. The general weighted Reward()
// function in reward.go isn't wired to any caller yet; if it is in the
// future, this threshold should be revisited since its Gamma-weighted
// success/failure boundary doesn't line up with 0.7.
const failureRewardThreshold = 0.7

// Progressive cooldown parameters (OmniRoute parity: a bandit-driven
// "auto-combo engine with progressive cooldown"). An arm that fails
// cooldownFailureStreak times in a row is excluded from Select for a
// backoff window that doubles with each additional consecutive failure,
// capped at cooldownMax. Any reward >= failureRewardThreshold resets the
// streak and clears the cooldown immediately — recovery is not gradual,
// only the backoff on repeated failure is.
const (
	cooldownFailureStreak = 3
	cooldownBase          = 5 * time.Second
	cooldownMax           = 5 * time.Minute
)

type armStats struct {
	pulls           int64
	sumReward       float64
	meanReward      float64
	consecutiveFail int
	cooldownUntil   time.Time
}

// recordOutcome updates an arm's failure streak and cooldown window after a
// reward is recorded. Call after pulls/sumReward/meanReward bookkeeping.
func (a *armStats) recordOutcome(reward float64, now time.Time) {
	if reward >= failureRewardThreshold {
		a.consecutiveFail = 0
		a.cooldownUntil = time.Time{}
		return
	}
	a.consecutiveFail++
	if a.consecutiveFail < cooldownFailureStreak {
		return
	}
	exp := a.consecutiveFail - cooldownFailureStreak
	if exp > 10 { // guard against a shift overflow on a very long failure streak
		exp = 10
	}
	backoff := cooldownBase * time.Duration(1<<uint(exp))
	if backoff > cooldownMax || backoff <= 0 {
		backoff = cooldownMax
	}
	a.cooldownUntil = now.Add(backoff)
}

func (a *armStats) inCooldown(now time.Time) bool {
	return !a.cooldownUntil.IsZero() && now.Before(a.cooldownUntil)
}

// eligible filters candidates down to arms not currently in cooldown. If
// every candidate is cooling down, it falls back to the full candidate
// list rather than returning none — routing must degrade to "ignore
// cooldown" before it degrades to "no model available."
func eligible[T any](candidates []T, model func(T) string, arms map[string]*armStats, now time.Time) []T {
	out := make([]T, 0, len(candidates))
	for _, c := range candidates {
		if a := arms[model(c)]; a != nil && a.inCooldown(now) {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return candidates
	}
	return out
}

// EpsilonGreedy implements ε-greedy multi-armed bandit selection.
// With probability ε it explores (random candidate); otherwise it exploits
// (highest mean reward seen so far).
type EpsilonGreedy struct {
	mu      sync.Mutex
	arms    map[string]*armStats
	epsilon float64
	regret  *RegretTracker
	now     func() time.Time
}

// NewEpsilonGreedy creates an ε-greedy bandit. epsilon ∈ [0, 1].
func NewEpsilonGreedy(epsilon float64, _ RewardWeights) *EpsilonGreedy {
	return &EpsilonGreedy{
		arms:    make(map[string]*armStats),
		epsilon: epsilon,
		regret:  &RegretTracker{},
		now:     time.Now,
	}
}

func (b *EpsilonGreedy) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	candidates = eligible(candidates, func(c routing.Candidate) string { return c.Model }, b.arms, b.now())
	if rand.Float64() < b.epsilon { //nolint:gosec
		return candidates[rand.Intn(len(candidates))].Model //nolint:gosec
	}
	best, bestReward := candidates[0].Model, math.Inf(-1)
	for _, c := range candidates {
		if a := b.arms[c.Model]; a != nil && a.meanReward > bestReward {
			bestReward = a.meanReward
			best = c.Model
		}
	}
	return best
}

func (b *EpsilonGreedy) Update(model string, reward float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.getArm(model)
	a.pulls++
	a.sumReward += reward
	a.meanReward = a.sumReward / float64(a.pulls)
	a.recordOutcome(reward, b.now())

	bestReward := math.Inf(-1)
	for _, arm := range b.arms {
		if arm.meanReward > bestReward {
			bestReward = arm.meanReward
		}
	}
	if !math.IsInf(bestReward, 1) {
		b.regret.Record(bestReward, reward)
	}
}

func (b *EpsilonGreedy) Regret() *RegretTracker { return b.regret }

func (b *EpsilonGreedy) getArm(model string) *armStats {
	a, ok := b.arms[model]
	if !ok {
		a = &armStats{}
		b.arms[model] = a
	}
	return a
}

// UCB implements the Upper Confidence Bound algorithm.
// UCB score = mean_reward + c * sqrt(ln(totalPulls) / armPulls).
// Untried arms are always selected first (score = +∞).
type UCB struct {
	mu         sync.Mutex
	arms       map[string]*armStats
	c          float64
	totalPulls int64
	regret     *RegretTracker
	now        func() time.Time
}

// NewUCB creates a UCB bandit. c is the exploration coefficient (typical: 1–2).
func NewUCB(c float64, _ RewardWeights) *UCB {
	return &UCB{
		arms:   make(map[string]*armStats),
		c:      c,
		regret: &RegretTracker{},
		now:    time.Now,
	}
}

func (b *UCB) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	candidates = eligible(candidates, func(c routing.Candidate) string { return c.Model }, b.arms, b.now())
	best, bestScore := "", math.Inf(-1)
	for _, c := range candidates {
		var score float64
		a := b.arms[c.Model]
		if a == nil || a.pulls == 0 {
			score = math.Inf(1)
		} else {
			exploration := b.c * math.Sqrt(math.Log(float64(b.totalPulls+1))/float64(a.pulls))
			score = a.meanReward + exploration
		}
		if score > bestScore {
			bestScore = score
			best = c.Model
		}
	}
	return best
}

func (b *UCB) Update(model string, reward float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.getArm(model)
	a.pulls++
	a.sumReward += reward
	a.meanReward = a.sumReward / float64(a.pulls)
	a.recordOutcome(reward, b.now())
	b.totalPulls++

	bestReward := math.Inf(-1)
	for _, arm := range b.arms {
		if arm.meanReward > bestReward {
			bestReward = arm.meanReward
		}
	}
	if !math.IsInf(bestReward, 1) {
		b.regret.Record(bestReward, reward)
	}
}

func (b *UCB) Regret() *RegretTracker { return b.regret }

func (b *UCB) getArm(model string) *armStats {
	a, ok := b.arms[model]
	if !ok {
		a = &armStats{}
		b.arms[model] = a
	}
	return a
}
