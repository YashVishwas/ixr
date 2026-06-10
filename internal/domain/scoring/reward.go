package scoring

// RewardWeights defines the coefficients for the reward function.
type RewardWeights struct {
	Alpha float64 // weight for latency (1 - normLatency)
	Beta  float64 // weight for cost    (1 - normCost)
	Gamma float64 // weight for success rate
	Delta float64 // weight for quality score
}

// DefaultRewardWeights is a balanced starting point for adaptive routing.
var DefaultRewardWeights = RewardWeights{
	Alpha: 0.3,
	Beta:  0.2,
	Gamma: 0.4,
	Delta: 0.1,
}

// Reward computes a scalar reward in [0, 1] for a single model invocation.
// normLatency and normCost are min-max normalized in [0, 1] across the candidate set.
// quality is a quality score in [0, 1]; pass 0 when unknown.
func Reward(latencyMS, costUSD float64, success bool, quality, normLatency, normCost float64, w RewardWeights) float64 {
	_ = latencyMS // raw values are for callers to normalize; normalized forms are used here
	_ = costUSD
	var sv float64
	if success {
		sv = 1.0
	}
	return w.Alpha*(1.0-normLatency) + w.Beta*(1.0-normCost) + w.Gamma*sv + w.Delta*quality
}
