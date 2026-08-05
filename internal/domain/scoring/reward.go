package scoring

import "github.com/YashVishwas/ixr/pkg/schema"

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

// QualityFromFinishReason derives a coarse, zero-cost quality signal from a
// completion's finish reason — every CallEvent already carries this for
// free, no extra scoring model or provider call needed. This is the
// docs/ADAPTIVE.md "quality_score (phase 2c)" term's first, cheapest
// implementation.
//
// Returns 0 when choices is empty (e.g. a failed call with no response), so
// callers that don't populate a response see no quality contribution rather
// than a guessed default — matching the reward formula's own convention of
// "pass 0 when unknown".
func QualityFromFinishReason(choices []schema.Choice) float64 {
	if len(choices) == 0 {
		return 0
	}
	switch choices[0].FinishReason {
	case "stop", "tool_calls":
		return 1.0 // clean, intentional completion
	case "length":
		return 0.3 // truncated — usually a worse answer, but not worthless
	case "content_filter":
		return 0.0 // refused/blocked — no useful answer at all
	default:
		return 0.5 // unrecognized/provider-specific reason — neutral, not a guess either way
	}
}
