package cost

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestForUsage_KnownModel(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	if got.InputUSD != 3 || got.OutputUSD != 15 || got.TotalUSD != 18 {
		t.Fatalf("got %+v, want input=3 output=15 total=18", got)
	}
}

func TestForUsage_UnknownModel(t *testing.T) {
	got := ForUsage("some-model-not-in-catalog", schema.Usage{PromptTokens: 1000, CompletionTokens: 1000})
	if got.TotalUSD != 0 {
		t.Fatalf("expected zero cost for unpriced model, got %+v", got)
	}
}

func TestForUsage_ZeroTokens(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", schema.Usage{})
	if got.TotalUSD != 0 {
		t.Fatalf("expected zero cost for zero tokens, got %+v", got)
	}
}

// TestForUsage_CacheReadDiscounted is the regression test for the gap this
// closes: PromptTokens used to be priced uniformly at the full input rate,
// overstating cost for any call with cache activity. A cache read must cost
// less than the same token count priced as regular input.
func TestForUsage_CacheReadDiscounted(t *testing.T) {
	allRegular := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000})
	allCached := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000, CacheReadInputTokens: 1_000_000})

	if allCached.InputUSD >= allRegular.InputUSD {
		t.Fatalf("expected a fully cache-read call to cost less than the same token count as regular input: cached=%.4f regular=%.4f", allCached.InputUSD, allRegular.InputUSD)
	}
	// claude-sonnet-4-6 input rate is $3/1M — a fully cache-read 1M-token
	// call should be priced at 10% of that ($0.30), byte-for-byte.
	want := 0.30
	if allCached.InputUSD < want-0.0001 || allCached.InputUSD > want+0.0001 {
		t.Fatalf("cache-read pricing: got %.4f, want %.4f (10%% of the $3/1M input rate)", allCached.InputUSD, want)
	}
}

// TestForUsage_CacheCreationCostsMore confirms cache writes are priced
// above the regular input rate, not below it — the opposite direction from
// a cache read, and easy to get backwards.
func TestForUsage_CacheCreationCostsMore(t *testing.T) {
	allRegular := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000})
	allCreation := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000, CacheCreationInputTokens: 1_000_000})

	if allCreation.InputUSD <= allRegular.InputUSD {
		t.Fatalf("expected a cache-creation call to cost more than the same token count as regular input: creation=%.4f regular=%.4f", allCreation.InputUSD, allRegular.InputUSD)
	}
	want := 3.75 // 1.25x the $3/1M input rate
	if allCreation.InputUSD < want-0.0001 || allCreation.InputUSD > want+0.0001 {
		t.Fatalf("cache-creation pricing: got %.4f, want %.4f (1.25x the $3/1M input rate)", allCreation.InputUSD, want)
	}
}

// TestForUsage_MixedRegularAndCacheTokens confirms the three-way split
// (regular / cache-read / cache-creation) is priced independently, not
// collapsed into a single rate.
func TestForUsage_MixedRegularAndCacheTokens(t *testing.T) {
	// 500k regular + 300k cache-read + 200k cache-creation = 1M PromptTokens.
	got := ForUsage("claude-sonnet-4-6", schema.Usage{
		PromptTokens:             1_000_000,
		CacheReadInputTokens:     300_000,
		CacheCreationInputTokens: 200_000,
	})
	// (500_000*1.0 + 200_000*1.25 + 300_000*0.1) / 1_000_000 * $3
	// = (500_000 + 250_000 + 30_000) / 1_000_000 * 3 = 780_000/1_000_000*3 = 2.34
	want := 2.34
	if got.InputUSD < want-0.0001 || got.InputUSD > want+0.0001 {
		t.Fatalf("mixed pricing: got %.4f, want %.4f", got.InputUSD, want)
	}
}

// TestForUsage_NonAnthropicProvider_Unaffected confirms usage with no cache
// fields set (any non-Anthropic provider, or an Anthropic call that never
// hit a cache_control block) prices exactly as it did before this change.
func TestForUsage_NonAnthropicProvider_Unaffected(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", schema.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	if got.InputUSD != 3 || got.OutputUSD != 15 || got.TotalUSD != 18 {
		t.Fatalf("got %+v, want input=3 output=15 total=18 (unchanged from pre-cache-aware pricing)", got)
	}
}

// TestForUsage_CacheCountsExceedPromptTokens_DefensiveFallback guards
// against a hypothetical upstream miscount (cache token counts summing to
// more than PromptTokens) producing a negative regular-token count.
func TestForUsage_CacheCountsExceedPromptTokens_DefensiveFallback(t *testing.T) {
	got := ForUsage("claude-sonnet-4-6", schema.Usage{
		PromptTokens:         100,
		CacheReadInputTokens: 500, // inconsistent with PromptTokens — shouldn't happen, but must not go negative
	})
	if got.InputUSD < 0 {
		t.Fatalf("expected non-negative cost even with an inconsistent cache count, got %.4f", got.InputUSD)
	}
}

// TestForUsage_DeepSeekCacheHitDiscounted proves prompt-cache pricing isn't
// Anthropic-only: DeepSeek's disk-backed KV cache reports hits into the
// same CacheReadInputTokens field, and deepseek-v3-0324's catalog entry
// configures its own (much steeper, no-write-premium) discount ratio.
func TestForUsage_DeepSeekCacheHitDiscounted(t *testing.T) {
	allRegular := ForUsage("deepseek-v3-0324", schema.Usage{PromptTokens: 1_000_000})
	allCached := ForUsage("deepseek-v3-0324", schema.Usage{PromptTokens: 1_000_000, CacheReadInputTokens: 1_000_000})

	if allCached.InputUSD >= allRegular.InputUSD {
		t.Fatalf("expected a fully cache-hit DeepSeek call to cost less than regular input: cached=%.4f regular=%.4f", allCached.InputUSD, allRegular.InputUSD)
	}
	// deepseek-v3-0324 input rate is $0.27/1M, cache-hit rate is 0.1x that.
	want := 0.27 * 0.1
	if allCached.InputUSD < want-0.0001 || allCached.InputUSD > want+0.0001 {
		t.Fatalf("DeepSeek cache-hit pricing: got %.4f, want %.4f", allCached.InputUSD, want)
	}
}

// TestForUsage_DeepSeekCacheMiss_NoWritePremium confirms DeepSeek's cache
// misses (CacheCreationInputTokens) price at the plain input rate, not a
// premium — unlike Anthropic, populating the cache costs nothing extra.
func TestForUsage_DeepSeekCacheMiss_NoWritePremium(t *testing.T) {
	allRegular := ForUsage("deepseek-v3-0324", schema.Usage{PromptTokens: 1_000_000})
	allCreation := ForUsage("deepseek-v3-0324", schema.Usage{PromptTokens: 1_000_000, CacheCreationInputTokens: 1_000_000})

	if allCreation.InputUSD != allRegular.InputUSD {
		t.Fatalf("expected DeepSeek cache-creation tokens to price the same as regular input (no write premium): creation=%.4f regular=%.4f", allCreation.InputUSD, allRegular.InputUSD)
	}
}

// TestForUsage_GeminiCacheHitDiscounted mirrors the DeepSeek case for
// Gemini's implicit context caching.
func TestForUsage_GeminiCacheHitDiscounted(t *testing.T) {
	allRegular := ForUsage("gemini-3.1-pro", schema.Usage{PromptTokens: 1_000_000})
	allCached := ForUsage("gemini-3.1-pro", schema.Usage{PromptTokens: 1_000_000, CacheReadInputTokens: 1_000_000})

	if allCached.InputUSD >= allRegular.InputUSD {
		t.Fatalf("expected a fully cache-hit Gemini call to cost less than regular input: cached=%.4f regular=%.4f", allCached.InputUSD, allRegular.InputUSD)
	}
	// gemini-3.1-pro input rate is $2/1M, cache-hit rate is 0.25x that.
	want := 2 * 0.25
	if allCached.InputUSD < want-0.0001 || allCached.InputUSD > want+0.0001 {
		t.Fatalf("Gemini cache-hit pricing: got %.4f, want %.4f", allCached.InputUSD, want)
	}
}

// TestForUsage_ModelWithNoCacheRatesConfigured_NoDiscountAssumed is the
// safety-net test for the per-model-configured design: a model that never
// had cache rates set (llama-4-scout has no prompt-caching support at all)
// must not silently inherit some other provider's discount ratio if cache
// token counts ever showed up in its usage — better to price it as regular
// input (no discount, no premium) than guess wrong.
func TestForUsage_ModelWithNoCacheRatesConfigured_NoDiscountAssumed(t *testing.T) {
	got := ForUsage("llama-4-scout", schema.Usage{PromptTokens: 1_000_000, CacheReadInputTokens: 1_000_000})
	want := ForUsage("llama-4-scout", schema.Usage{PromptTokens: 1_000_000})
	if got.InputUSD != want.InputUSD {
		t.Fatalf("expected an unconfigured model's cache tokens to price at the plain input rate: got %.4f, want %.4f", got.InputUSD, want.InputUSD)
	}
}
