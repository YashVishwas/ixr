package routing

import "github.com/YashVishwas/ixr/internal/domain/circuitbreaker"

// Filter removes models that violate hard constraints:
//   - blended cost > hint.MaxCostUSDPer1M (when cap > 0)
//   - circuit breaker is open (when cb is non-nil)
func Filter(models []ModelCard, hint TaskHint, cb *circuitbreaker.Registry) []ModelCard {
	inputShare := estimateInputShare(hint.PromptChars)
	var out []ModelCard
	for _, m := range models {
		if hint.MaxCostUSDPer1M > 0 && blendedCost(m, inputShare) > hint.MaxCostUSDPer1M {
			continue
		}
		if cb != nil && !cb.IsAllowed(m.ID) {
			continue
		}
		out = append(out, m)
	}
	return out
}
