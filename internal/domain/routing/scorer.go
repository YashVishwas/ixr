package routing

import "github.com/YashVishwas/ixr/pkg/store"

// Score returns a utility score (higher = better) for model m.
//
// When stats.SampleCount >= 10, live performance data is used; otherwise the
// ModelCard's static priors fill in. weights = [costWeight, latencyWeight,
// reliabilityWeight] come from the per-intent RoutingPolicy.
func Score(m ModelCard, hint TaskHint, stats store.ModelStats, weights [3]float64, inputShare float64) float64 {
	var successRate, latencySec float64
	if stats.SampleCount >= 10 {
		successRate = stats.SuccessRate
		latencySec = stats.P50LatencyMS / 1000.0
	} else {
		successRate = 1.0 - m.FailureRate
		latencySec = m.LatencySec
	}

	capability := capabilityMatch(m, hint)
	cost := blendedCost(m, inputShare)
	failurePenalty := 1.0 - successRate

	return wCapability*capability -
		weights[0]*cost -
		weights[1]*latencySec -
		weights[2]*failurePenalty
}
