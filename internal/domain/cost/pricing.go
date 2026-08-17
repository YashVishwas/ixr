// Package cost computes the USD cost of a call given a model and token counts.
package cost

import (
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Anthropic prices ephemeral (5-minute TTL) prompt-cache activity relative
// to the normal input rate: a cache write costs more (the model actually
// processes and stores the prefix), a cache read costs much less — the
// whole point of caching. These multipliers only apply to the portion of
// PromptTokens attributed to CacheCreationInputTokens/CacheReadInputTokens;
// for any provider that doesn't report those (they default to 0 —
// non-Anthropic providers, or Anthropic calls that didn't hit a cache_control
// block), this is a no-op and pricing is exactly the pre-existing behavior.
const (
	cacheWriteMultiplier = 1.25
	cacheReadMultiplier  = 0.1
)

// ForUsage prices usage against the routing catalog's per-model rates.
// Returns a zero CostBreakdown when the model has no catalog pricing
// entry — callers (budget enforcement, billing) should treat cost <= 0 as
// "unpriced", not "free".
func ForUsage(model string, usage schema.Usage) schema.CostBreakdown {
	card, ok := routing.Lookup(model)
	if !ok {
		return schema.CostBreakdown{}
	}

	regular := usage.PromptTokens - usage.CacheReadInputTokens - usage.CacheCreationInputTokens
	cacheRead, cacheCreation := usage.CacheReadInputTokens, usage.CacheCreationInputTokens
	if regular < 0 {
		// Defensive only — a caller reporting cache counts that exceed
		// PromptTokens means something upstream miscounted; price the
		// whole thing as regular input rather than let cost go negative.
		regular = usage.PromptTokens
		cacheRead, cacheCreation = 0, 0
	}

	weightedInput := float64(regular) +
		float64(cacheCreation)*cacheWriteMultiplier +
		float64(cacheRead)*cacheReadMultiplier

	in := weightedInput / 1_000_000 * card.InputUSDPer1M
	out := float64(usage.CompletionTokens) / 1_000_000 * card.OutputUSDPer1M
	return schema.CostBreakdown{InputUSD: in, OutputUSD: out, TotalUSD: in + out}
}
