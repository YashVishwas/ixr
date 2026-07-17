// Package cost computes the USD cost of a call given a model and token counts.
package cost

import (
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// ForUsage prices tokensIn/tokensOut against the routing catalog's per-model
// rates. Returns a zero CostBreakdown when the model has no catalog pricing
// entry — callers (budget enforcement, billing) should treat cost <= 0 as
// "unpriced", not "free".
func ForUsage(model string, tokensIn, tokensOut int) schema.CostBreakdown {
	card, ok := routing.Lookup(model)
	if !ok {
		return schema.CostBreakdown{}
	}
	in := float64(tokensIn) / 1_000_000 * card.InputUSDPer1M
	out := float64(tokensOut) / 1_000_000 * card.OutputUSDPer1M
	return schema.CostBreakdown{InputUSD: in, OutputUSD: out, TotalUSD: in + out}
}
