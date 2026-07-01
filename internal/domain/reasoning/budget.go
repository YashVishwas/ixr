// Package reasoning provides token budget adjustments for models that consume
// tokens on internal chain-of-thought before producing visible output.
//
// Models like o3 and Gemini thinking variants may use 90–98% of max_tokens
// on reasoning steps invisible to the caller. Without adjustment, a request
// for 1,000 output tokens can yield as few as 20–50 visible tokens.
//
// AdjustTokenBudget scales max_tokens upward so the caller reliably receives
// the output length they asked for. The adjustment is applied to a copy of
// the request — the original is never mutated.
package reasoning

import (
	"strings"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// reasoningOverhead maps model ID prefixes to the estimated fraction of
// max_tokens consumed by internal reasoning (0–1). Conservative values —
// operators can tune via IXR_REASONING_OVERHEAD_<MODEL> env vars if needed.
//
// Source: empirical reports from production deployments and OpenAI/Google docs.
var reasoningOverhead = []modelOverhead{
	// OpenAI reasoning models
	{prefix: "o3-mini", overhead: 0.80},
	{prefix: "o3", overhead: 0.90},
	{prefix: "o1-mini", overhead: 0.75},
	{prefix: "o1", overhead: 0.85},
	// Google Gemini thinking variants
	{prefix: "gemini-2.0-flash-thinking", overhead: 0.85},
	{prefix: "gemini-2.5-flash-thinking", overhead: 0.85},
	{prefix: "gemini-2.5-pro", overhead: 0.80},
	// DeepSeek R1 series (chain-of-thought)
	{prefix: "deepseek-r1", overhead: 0.82},
}

type modelOverhead struct {
	prefix   string
	overhead float64
}

// overheadFor returns the reasoning overhead fraction for the given model ID,
// or 0 if the model is not a known reasoning model.
func overheadFor(model string) float64 {
	lower := strings.ToLower(model)
	// Match longest prefix first to avoid o3 matching o3-mini incorrectly.
	best := ""
	var bestOverhead float64
	for _, m := range reasoningOverhead {
		if strings.HasPrefix(lower, m.prefix) && len(m.prefix) > len(best) {
			best = m.prefix
			bestOverhead = m.overhead
		}
	}
	return bestOverhead
}

// IsReasoningModel reports whether the given model ID is known to consume
// tokens on internal reasoning before producing visible output.
func IsReasoningModel(model string) bool {
	return overheadFor(model) > 0
}

// AdjustTokenBudget returns a copy of req with MaxTokens scaled up to
// compensate for the model's internal reasoning overhead. If the model is not
// a reasoning model, or if MaxTokens is 0 (unlimited), req is returned as-is.
//
// Formula: adjusted = ceil(requested / (1 - overhead))
// Example: o3 with overhead=0.90, caller requests 1000 visible tokens
//   → adjusted = ceil(1000 / 0.10) = 10000 passed to provider
//   → ~9000 consumed by reasoning, ~1000 visible to caller
//
// The adjusted value is capped at 100,000 tokens to guard against runaway
// values when overhead is misconfigured.
func AdjustTokenBudget(req *schema.RequestEnvelope) *schema.RequestEnvelope {
	overhead := overheadFor(req.Model)
	if overhead == 0 || req.MaxTokens <= 0 {
		return req
	}

	visible := float64(req.MaxTokens)
	adjusted := int(visible/(1.0-overhead) + 0.999) // ceiling division

	const maxAdjusted = 100_000
	if adjusted > maxAdjusted {
		adjusted = maxAdjusted
	}

	copy := *req
	copy.MaxTokens = adjusted
	return &copy
}
