package scoring

import (
	"math"
	"math/rand"
	"sync"

	"github.com/YashVishwas/ixr/internal/domain/routing"
)

// Bandit is the interface for adaptive model-selection algorithms.
type Bandit interface {
	Select(candidates []routing.Candidate) string
	Update(model string, reward float64)
	Regret() *RegretTracker
}

type armStats struct {
	pulls      int64
	sumReward  float64
	meanReward float64
}

// EpsilonGreedy implements ε-greedy multi-armed bandit selection.
// With probability ε it explores (random candidate); otherwise it exploits
// (highest mean reward seen so far).
type EpsilonGreedy struct {
	mu      sync.Mutex
	arms    map[string]*armStats
	epsilon float64
	regret  *RegretTracker
}

// NewEpsilonGreedy creates an ε-greedy bandit. epsilon ∈ [0, 1].
func NewEpsilonGreedy(epsilon float64, _ RewardWeights) *EpsilonGreedy {
	return &EpsilonGreedy{
		arms:    make(map[string]*armStats),
		epsilon: epsilon,
		regret:  &RegretTracker{},
	}
}

func (b *EpsilonGreedy) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
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
}

// NewUCB creates a UCB bandit. c is the exploration coefficient (typical: 1–2).
func NewUCB(c float64, _ RewardWeights) *UCB {
	return &UCB{
		arms:   make(map[string]*armStats),
		c:      c,
		regret: &RegretTracker{},
	}
}

func (b *UCB) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
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
