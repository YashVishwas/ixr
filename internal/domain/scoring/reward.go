package scoring

// RewardWeights controls the adaptive routing reward calculation.
type RewardWeights struct {
	Latency     float64
	Cost        float64
	Reliability float64
	Quality     float64
}

// RewardInput captures observed request outcome metrics.
type RewardInput struct {
	LatencyMS    float64
	CostPerToken float64
	SuccessRate  float64
	QualityScore float64
}

// Reward computes a bounded positive reward. Higher is better.
func Reward(weights RewardWeights, input RewardInput) float64 {
	if weights == (RewardWeights{}) {
		weights = RewardWeights{Latency: 0.25, Cost: 0.25, Reliability: 0.25, Quality: 0.25}
	}
	latency := 0.0
	if input.LatencyMS > 0 {
		latency = 1 / input.LatencyMS
	}
	cost := 1 - input.CostPerToken
	if cost < 0 {
		cost = 0
	}
	return weights.Latency*latency +
		weights.Cost*cost +
		weights.Reliability*clamp(input.SuccessRate) +
		weights.Quality*clamp(input.QualityScore)
}

func clamp(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// reward computes: α*(1/latency_ms) + β*(1-cost_per_token) + γ*success_rate + δ*quality_score.
// α, β, γ, δ are learned per intent by the bandit algorithm.