package scoring

// RegretTracker accumulates regret for adaptive routing experiments.
type RegretTracker struct {
	Cumulative float64
	Count      int
}

// Observe adds one request's regret. Negative regret is clamped to zero.
func (r *RegretTracker) Observe(optimalReward, chosenReward float64) {
	regret := optimalReward - chosenReward
	if regret < 0 {
		regret = 0
	}
	r.Cumulative += regret
	r.Count++
}

// Average returns average regret per observed request.
func (r RegretTracker) Average() float64 {
	if r.Count == 0 {
		return 0
	}
	return r.Cumulative / float64(r.Count)
}

// regret tracks cumulative regret = sum(optimal_reward - chosen_reward) across all requests.
// Lower cumulative regret means the algorithm is learning faster.
// This is the north star metric for v2 routing quality.