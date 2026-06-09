package routing

import "sort"

// ModelStats is the scoring engine's hot-path view of provider/model performance.
type ModelStats struct {
	CostUSDPer1M  float64
	P50LatencyMS  float64
	P95LatencyMS  int
	SuccessRate   float64
	CircuitOpen   bool
	Observations  int
	LastUpdatedMS int64
}

// Weights are deterministic v1 scoring weights. Higher reliability weight means
// failure hurts more. Lower score wins.
type Weights struct {
	Cost        float64
	Latency     float64
	Reliability float64
}

// ScoreCandidates scores and sorts candidates by ascending score.
func ScoreCandidates(candidates []Candidate, stats map[string]ModelStats, weights Weights) []Candidate {
	if weights == (Weights{}) {
		weights = Weights{Cost: 0.33, Latency: 0.33, Reliability: 0.34}
	}
	maxCost := 0.0
	maxLatency := 0.0
	for _, c := range candidates {
		s := stats[c.Model]
		if s.CostUSDPer1M > maxCost {
			maxCost = s.CostUSDPer1M
		}
		if s.P50LatencyMS > maxLatency {
			maxLatency = s.P50LatencyMS
		}
	}

	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	for i := range out {
		s := stats[out[i].Model]
		out[i].Score = weights.Cost*norm(s.CostUSDPer1M, maxCost) +
			weights.Latency*norm(s.P50LatencyMS, maxLatency) +
			weights.Reliability*(1-s.SuccessRate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score < out[j].Score
	})
	return out
}

func norm(v, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return v / max
}

// scorer computes score(model) = w1*normalized_cost + w2*normalized_latency + w3*(1-success_rate).
// Lower score = better candidate.
// Weights are per-intent and loaded from the policy store at route time.