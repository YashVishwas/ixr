package routing

// BuildFallbackChain returns the next best candidates after the selected model.
func BuildFallbackChain(scored []Candidate, selectedModel string, limit int) []Candidate {
	if limit <= 0 {
		limit = 2
	}
	out := make([]Candidate, 0, limit)
	for _, c := range scored {
		if c.Model == selectedModel {
			continue
		}
		out = append(out, c)
		if len(out) == limit {
			break
		}
	}
	return out
}

// fallback builds the fallback chain from the next two lowest-scored candidates
// after the primary model is selected.