package routing

// RouteWithDecision runs the scoring pipeline and returns a RoutingDecision with
// the primary model plus up to 2 fallback candidates. Preserves the same scoring
// logic as Route so existing tests remain unaffected.
func RouteWithDecision(hint TaskHint) RoutingDecision {
	candidates := scoreAll(hint, catalog)
	if len(candidates) == 0 {
		return RoutingDecision{}
	}
	primary := candidates[0].Model
	return RoutingDecision{
		Model:         primary,
		FallbackChain: BuildFallbackChain(candidates, primary, 2),
	}
}

// BuildFallbackChain returns up to n candidates from the scored list, excluding
// the primary model. candidates must already be sorted by descending score.
func BuildFallbackChain(candidates []Candidate, primary string, n int) []Candidate {
	var chain []Candidate
	for _, c := range candidates {
		if c.Model == primary {
			continue
		}
		chain = append(chain, c)
		if len(chain) >= n {
			break
		}
	}
	return chain
}
