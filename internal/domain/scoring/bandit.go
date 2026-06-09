package scoring

import (
	"math"
	"math/rand"
	"sort"
)

// ArmStats stores aggregate reward observations for one model.
type ArmStats struct {
	Model       string
	Pulls       int
	TotalReward float64
}

// MeanReward returns average observed reward.
func (a ArmStats) MeanReward() float64 {
	if a.Pulls == 0 {
		return 0
	}
	return a.TotalReward / float64(a.Pulls)
}

// EpsilonGreedy selects a model. With probability epsilon it explores.
func EpsilonGreedy(arms []ArmStats, epsilon float64, rng *rand.Rand) string {
	if len(arms) == 0 {
		return ""
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if epsilon > 0 && rng.Float64() < epsilon {
		return arms[rng.Intn(len(arms))].Model
	}
	sorted := append([]ArmStats(nil), arms...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].MeanReward() > sorted[j].MeanReward()
	})
	return sorted[0].Model
}

// UCB selects a model using upper confidence bound. Untried arms are preferred first.
func UCB(arms []ArmStats, exploration float64) string {
	if len(arms) == 0 {
		return ""
	}
	totalPulls := 0
	for _, arm := range arms {
		if arm.Pulls == 0 {
			return arm.Model
		}
		totalPulls += arm.Pulls
	}
	if exploration <= 0 {
		exploration = 2
	}
	bestModel := ""
	bestScore := math.Inf(-1)
	for _, arm := range arms {
		score := arm.MeanReward() + math.Sqrt(exploration*math.Log(float64(totalPulls))/float64(arm.Pulls))
		if score > bestScore {
			bestScore = score
			bestModel = arm.Model
		}
	}
	return bestModel
}

// bandit implements epsilon-greedy and UCB adaptive routing algorithms.
// Both run in shadow mode before either becomes the live algorithm.
// The better one is chosen by minimizing cumulative regret on real traffic.