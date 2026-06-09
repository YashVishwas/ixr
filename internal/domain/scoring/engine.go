// Package scoring contains the adaptive scoring engine.
// v1: deterministic weighted scoring driven by per-intent weights from the policy store.
// v2: bandit algorithms (epsilon-greedy and UCB) that learn from real traffic.
package scoring

import "sort"

// Candidate is a routable provider/model pair.
type Candidate struct {
	Provider string
	Model    string
}

// ModelSnapshot is the precomputed hot-path model performance record.
type ModelSnapshot struct {
	Model        string
	Cost        float64
	P50Latency  float64
	P95Latency  float64
	SuccessRate float64
	CircuitOpen bool
}

// Request describes the deterministic v1 scoring input.
type Request struct {
	Candidates   []Candidate
	Stats        map[string]ModelSnapshot
	Weights      Weights
	MaxCost       float64
	MaxLatencyP95 float64
	FallbackLimit int
}

// Weights controls deterministic v1 scoring. Lower score wins.
type Weights struct {
	Cost        float64
	Latency     float64
	Reliability float64
}

// Decision is the selected model plus fallback chain.
type Decision struct {
	Primary   Candidate
	Fallbacks []Candidate
	Score     float64
}

// Engine computes deterministic v1 routing decisions.
type Engine struct{}

// Decide filters candidates, scores them, and returns the best candidate with fallbacks.
func (Engine) Decide(req Request) (Decision, bool) {
	scored := score(req)
	if len(scored) == 0 {
		return Decision{}, false
	}
	limit := req.FallbackLimit
	if limit <= 0 {
		limit = 2
	}
	fallbacks := make([]Candidate, 0, limit)
	for _, item := range scored[1:] {
		fallbacks = append(fallbacks, item.candidate)
		if len(fallbacks) == limit {
			break
		}
	}
	return Decision{Primary: scored[0].candidate, Fallbacks: fallbacks, Score: scored[0].score}, true
}

type scoredCandidate struct {
	candidate Candidate
	score     float64
}

func score(req Request) []scoredCandidate {
	weights := req.Weights
	if weights == (Weights{}) {
		weights = Weights{Cost: 0.33, Latency: 0.33, Reliability: 0.34}
	}

	maxCost := 0.0
	maxLatency := 0.0
	for _, c := range req.Candidates {
		s := req.Stats[c.Model]
		if req.MaxCost > 0 && s.Cost > req.MaxCost {
			continue
		}
		if req.MaxLatencyP95 > 0 && s.P95Latency > req.MaxLatencyP95 {
			continue
		}
		if s.CircuitOpen {
			continue
		}
		if s.Cost > maxCost {
			maxCost = s.Cost
		}
		if s.P50Latency > maxLatency {
			maxLatency = s.P50Latency
		}
	}

	out := make([]scoredCandidate, 0, len(req.Candidates))
	for _, c := range req.Candidates {
		s := req.Stats[c.Model]
		if req.MaxCost > 0 && s.Cost > req.MaxCost {
			continue
		}
		if req.MaxLatencyP95 > 0 && s.P95Latency > req.MaxLatencyP95 {
			continue
		}
		if s.CircuitOpen {
			continue
		}
		score := weights.Cost*normalize(s.Cost, maxCost) +
			weights.Latency*normalize(s.P50Latency, maxLatency) +
			weights.Reliability*(1-clamp(s.SuccessRate))
		out = append(out, scoredCandidate{candidate: c, score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].score < out[j].score
	})
	return out
}

func normalize(v, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return v / max
}
