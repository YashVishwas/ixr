package scoring

import (
	"math"
	"sync/atomic"
)

// RegretTracker accumulates cumulative regret = Σ(optimal_reward − chosen_reward).
// All methods are safe for concurrent use.
type RegretTracker struct {
	cumBits atomic.Uint64 // math.Float64bits encoding of the cumulative sum
	count   atomic.Int64
}

// Record adds one observation: optimal is the best available reward in the candidate
// set; chosen is the reward actually received.
func (t *RegretTracker) Record(optimal, chosen float64) {
	regret := math.Max(0, optimal-chosen)
	for {
		old := t.cumBits.Load()
		next := math.Float64bits(math.Float64frombits(old) + regret)
		if t.cumBits.CompareAndSwap(old, next) {
			break
		}
	}
	t.count.Add(1)
}

// Cumulative returns the total regret accumulated so far.
func (t *RegretTracker) Cumulative() float64 {
	return math.Float64frombits(t.cumBits.Load())
}

// AverageRegret returns cumulative regret divided by the observation count.
func (t *RegretTracker) AverageRegret() float64 {
	n := t.count.Load()
	if n == 0 {
		return 0
	}
	return t.Cumulative() / float64(n)
}
