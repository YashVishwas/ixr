// Package cost computes the USD cost of a call given a model and token counts.
package cost

import (
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// ForUsage prices usage against the routing catalog's per-model rates.
// Returns a zero CostBreakdown when the model has no catalog pricing
// entry — callers (budget enforcement, billing) should treat cost <= 0 as
// "unpriced", not "free".
//
// Prompt-cache activity (Anthropic cache_control, Gemini implicit context
// caching, DeepSeek's disk-backed KV cache) is priced per-model via
// ModelCard.CachedInputUSDPer1M/CacheWriteUSDPer1M rather than a single
// global ratio — the providers' actual cache economics differ (Anthropic
// charges a write premium, DeepSeek/Gemini don't) and a global constant
// would misprice whichever provider it wasn't tuned for. A model with
// neither field configured falls back to InputUSDPer1M for that portion —
// no discount (or premium) assumed unless a real one is set on the catalog
// entry. For any provider/call that doesn't report cache token counts
// (CacheReadInputTokens/CacheCreationInputTokens default to 0), this is a
// no-op and pricing is exactly the plain-input-rate behavior.
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

	cachedRate := card.CachedInputUSDPer1M
	if cachedRate == 0 {
		cachedRate = card.InputUSDPer1M
	}
	writeRate := card.CacheWriteUSDPer1M
	if writeRate == 0 {
		writeRate = card.InputUSDPer1M
	}

	in := float64(regular)/1_000_000*card.InputUSDPer1M +
		float64(cacheCreation)/1_000_000*writeRate +
		float64(cacheRead)/1_000_000*cachedRate
	out := float64(usage.CompletionTokens) / 1_000_000 * card.OutputUSDPer1M
	return schema.CostBreakdown{InputUSD: in, OutputUSD: out, TotalUSD: in + out}
}
