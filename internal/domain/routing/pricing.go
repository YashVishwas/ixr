package routing

// PriceCard is a model's per-million-token input/output rate.
type PriceCard struct {
	InputUSDPer1M  float64
	OutputUSDPer1M float64
}

// pricingTable covers models that are commonly configured (see
// internal/adapters/config's provider defaults, demo-ixr.yaml, and
// knownContextWindows above) but aren't part of the curated auto-routing
// catalog — that catalog is deliberately small, since it's the candidate
// set scoreAll/Route pick from for model:"auto", not a pricing database.
// Lookup() falls back to this table so cost.ForUsage (and therefore budget
// enforcement) doesn't silently price real, by-name-requested models at $0
// just because they're not auto-routing candidates.
//
// Rates are a best-effort snapshot of published provider pricing pages as
// of mid-2026, gathered while fixing this table's absence — not billing-
// grade, and not kept in sync automatically. Re-verify against the
// provider's own pricing page before relying on this for anything beyond
// soft budget alerts. Free-tier-only providers (GitHub Models preview,
// OpenRouter's per-underlying-model rates, SambaNova) are intentionally
// omitted rather than guessed — $0 is the honest answer for those today,
// not a gap.
var pricingTable = map[string]PriceCard{
	// OpenAI
	"gpt-4o":        {2.50, 10.00},
	"gpt-4o-mini":   {0.15, 0.30},
	"gpt-4-turbo":   {10.00, 30.00},
	"gpt-4":         {30.00, 60.00},
	"gpt-3.5-turbo": {0.50, 1.50},
	"o1":            {15.00, 60.00},
	"o1-mini":       {1.10, 4.40},
	"o3":            {2.00, 8.00},
	"o3-mini":       {1.10, 4.40},

	// Anthropic — snapshot/legacy IDs not in the auto-routing catalog above
	// (which already prices the current claude-opus-4.7/claude-sonnet-4-6
	// aliases). claude-haiku-4-5-20251001 is the exact dated snapshot ID
	// Anthropic's API echoes back for the "claude-haiku-4-5" alias.
	"claude-3-5-sonnet-20241022": {3.00, 15.00},
	"claude-3-5-haiku-20241022":  {0.80, 4.00},
	"claude-3-opus-20240229":     {15.00, 75.00},
	"claude-opus-4-5":            {5.00, 25.00},
	"claude-sonnet-4-5":          {3.00, 15.00},
	"claude-haiku-4-5":           {1.00, 5.00},
	"claude-haiku-4-5-20251001":  {1.00, 5.00},

	// Google Gemini. 1.5-pro/1.5-flash/2.0-flash were retired June 2026 —
	// priced anyway so CallEvents already on disk referencing them still
	// cost correctly; current models follow.
	"gemini-1.5-pro":        {1.25, 5.00},
	"gemini-1.5-flash":      {0.075, 0.30},
	"gemini-2.0-flash":      {0.10, 0.40},
	"gemini-3.5-flash":      {1.50, 9.00},
	"gemini-3-flash":        {0.50, 3.00},
	"gemini-3.1-flash-lite": {0.25, 1.50},
	"gemini-2.5-flash-lite": {0.10, 0.40},

	// Meta / Llama via Groq's on-demand (paid) tier.
	"llama-3.3-70b-versatile": {0.59, 0.79},
	"llama-3.1-8b-instant":    {0.05, 0.08},

	// Mistral
	"mistral-large-latest": {2.00, 6.00},
	"mistral-small-latest": {0.10, 0.30},

	// DeepSeek. deepseek-chat/deepseek-coder deprecate 2026-07-24 in favor
	// of deepseek-v4-flash/deepseek-v4-pro; priced here at the standard
	// (cache-miss) input rate, not the cheaper cache-hit rate, since ixr
	// has no way to know which applied to a given call — better to
	// overestimate spend slightly than let a quota silently go unenforced.
	"deepseek-chat":     {0.14, 0.28},
	"deepseek-coder":    {0.14, 0.28},
	"deepseek-v4-flash": {0.14, 0.28},
	"deepseek-v4-pro":   {0.435, 0.87},

	// Cerebras on-demand tier (a separate free tier exists but is rate-limited).
	"gpt-oss-120b": {0.35, 0.75},
}

// lookupPricingTable returns pricing-only info for a model not present in
// the curated auto-routing catalog.
func lookupPricingTable(model string) (ModelCard, bool) {
	p, ok := pricingTable[model]
	if !ok {
		return ModelCard{}, false
	}
	return ModelCard{ID: model, InputUSDPer1M: p.InputUSDPer1M, OutputUSDPer1M: p.OutputUSDPer1M}, true
}
