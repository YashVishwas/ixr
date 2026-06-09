package routing

// Constraints are hard routing requirements. Zero values are unconstrained.
type Constraints struct {
	MaxCostUSDPer1M float64
	MaxLatencyMS    int
}

// FilterCandidates removes models that violate hard constraints or have open circuits.
func FilterCandidates(candidates []Candidate, stats map[string]ModelStats, constraints Constraints) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		s := stats[c.Model]
		if constraints.MaxCostUSDPer1M > 0 && s.CostUSDPer1M > constraints.MaxCostUSDPer1M {
			continue
		}
		if constraints.MaxLatencyMS > 0 && s.P95LatencyMS > constraints.MaxLatencyMS {
			continue
		}
		if s.CircuitOpen {
			continue
		}
		out = append(out, c)
	}
	return out
}
