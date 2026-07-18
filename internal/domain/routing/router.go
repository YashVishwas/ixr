// Package routing picks a provider + model for each request given a ParsedRequest.
// It orchestrates: filter → score → select → build fallback chain.
// Pure domain logic — no HTTP, no Redis, no external dependencies.
package routing

import (
	"math"
	"sort"
)

// RoutingDecision is the output of the routing engine for a single request.
type RoutingDecision struct {
	Provider      string
	Model         string
	FallbackChain []Candidate
}

// Candidate is a scored model eligible for routing (prefix-router fallback chain; phase 2).
type Candidate struct {
	Provider string
	Model    string
	Score    float64
}

const (
	wCapability = 1.00
	wCost       = 0.18
	wLatency    = 0.12
	wFailure    = 0.10
)

// TaskHint describes request preferences for automatic model selection.
type TaskHint struct {
	PromptChars int

	ReasoningScore    float64
	CodingScore       float64
	MathScore         float64
	MultilingualScore float64

	LatencySensitive bool
	MaxCostUSDPer1M  float64

	Tenant string
}

// ModelCard is static metadata for a routable model (cost, latency, capability priors).
type ModelCard struct {
	ID string

	InputUSDPer1M  float64
	OutputUSDPer1M float64

	LatencySec  float64
	FailureRate float64

	Reasoning    float64
	Coding       float64
	Math         float64
	Multilingual float64

	// ContextWindow is the model's maximum context length in tokens.
	// Used by SessionMiddleware to trim history before it overflows.
	ContextWindow int
}

var catalog = []ModelCard{
	{
		ID:             "claude-opus-4.7",
		InputUSDPer1M:  5,
		OutputUSDPer1M: 25,

		LatencySec:  1.8,
		FailureRate: 0.02,

		Reasoning:    0.98,
		Coding:       0.90,
		Math:         0.99,
		Multilingual: 0.88,

		ContextWindow: 200_000,
	},
	{
		ID:             "gpt-5.2",
		InputUSDPer1M:  1.5,
		OutputUSDPer1M: 14,

		LatencySec:  0.6,
		FailureRate: 0.025,

		Reasoning:    0.94,
		Coding:       0.93,
		Math:         0.95,
		Multilingual: 0.86,

		ContextWindow: 128_000,
	},
	{
		ID:             "gpt-5.3-codex",
		InputUSDPer1M:  1.75,
		OutputUSDPer1M: 14,

		LatencySec:  0.003,
		FailureRate: 0.03,

		Reasoning:    0.84,
		Coding:       0.98,
		Math:         0.88,
		Multilingual: 0.78,

		ContextWindow: 128_000,
	},
	{
		ID:             "gemini-3.1-pro",
		InputUSDPer1M:  2,
		OutputUSDPer1M: 12,

		LatencySec:  30.3,
		FailureRate: 0.022,

		Reasoning:    0.96,
		Coding:       0.88,
		Math:         1.00,
		Multilingual: 0.94,

		ContextWindow: 1_000_000,
	},
	{
		ID:             "deepseek-v3-0324",
		InputUSDPer1M:  0.27,
		OutputUSDPer1M: 1.10,

		LatencySec:  4,
		FailureRate: 0.035,

		Reasoning:    0.84,
		Coding:       0.78,
		Math:         0.88,
		Multilingual: 0.76,

		ContextWindow: 128_000,
	},
	{
		ID:             "llama-4-scout",
		InputUSDPer1M:  0.11,
		OutputUSDPer1M: 0.34,

		LatencySec:  0.33,
		FailureRate: 0.04,

		Reasoning:    0.76,
		Coding:       0.70,
		Math:         0.78,
		Multilingual: 0.74,

		ContextWindow: 10_000_000,
	},
	{
		ID:             "gemma-3-27b",
		InputUSDPer1M:  0.07,
		OutputUSDPer1M: 0.07,
		LatencySec:     0.72,
		FailureRate:    0.045,
		Reasoning:      0.68,
		Coding:         0.62,
		Math:           0.70,
		Multilingual:   0.72,
		ContextWindow:  128_000,
	},
}

// knownContextWindows covers widely-used models not in the routing catalog.
// Used by ContextWindowFor as a secondary lookup before falling back to the default.
var knownContextWindows = map[string]int{
	// OpenAI
	"gpt-4o":        128_000,
	"gpt-4o-mini":   128_000,
	"gpt-4-turbo":   128_000,
	"gpt-4":         8_192,
	"gpt-3.5-turbo": 16_385,
	"o1":            200_000,
	"o1-mini":       128_000,
	"o3":            200_000,
	"o3-mini":       200_000,
	// Anthropic
	"claude-3-5-sonnet-20241022": 200_000,
	"claude-3-5-haiku-20241022":  200_000,
	"claude-3-opus-20240229":     200_000,
	"claude-opus-4-5":            200_000,
	"claude-sonnet-4-5":          200_000,
	"claude-haiku-4-5":           200_000,
	// Google
	"gemini-1.5-pro":   1_000_000,
	"gemini-1.5-flash": 1_000_000,
	"gemini-2.0-flash": 1_000_000,
	// Meta / Llama
	"llama-4-maverick": 1_000_000,
	// Mistral
	"mistral-large-latest": 128_000,
	"mistral-small-latest": 128_000,
	// DeepSeek
	"deepseek-chat":  64_000,
	"deepseek-coder": 128_000,
}

// defaultContextWindow is used when a model is not in the catalog or knownContextWindows.
// 128k is the most common context window for modern frontier models.
const defaultContextWindow = 128_000

// ContextWindowFor returns the context window size in tokens for the given model ID.
// Checks the routing catalog first, then knownContextWindows, then returns the default.
func ContextWindowFor(model string) int {
	for _, card := range catalog {
		if card.ID == model {
			return card.ContextWindow
		}
	}
	if w, ok := knownContextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}

func clamp01(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	default:
		return x
	}
}

func minMax(values []float64) (float64, float64) {
	min := math.Inf(1)
	max := math.Inf(-1)

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	return min, max
}

func normalizeMinMax(v, min, max float64) float64 {
	if math.Abs(max-min) < 1e-9 {
		return 0
	}

	return clamp01((v - min) / (max - min))
}

func estimateInputShare(promptChars int) float64 {
	if promptChars <= 0 {
		return 0.45
	}

	x := float64(promptChars) / 8000.0
	return clamp01(0.25 + 0.5*x/(x+1))
}

func blendedCost(m ModelCard, inputShare float64) float64 {
	return inputShare*m.InputUSDPer1M +
		(1-inputShare)*m.OutputUSDPer1M
}

// capabilityMatch scores how well a model matches weighted task dimensions in [0, 1].
func capabilityMatch(m ModelCard, hint TaskHint) float64 {
	weights := []float64{
		hint.ReasoningScore,
		hint.CodingScore,
		hint.MathScore,
		hint.MultilingualScore,
	}

	sum := 0.0
	for _, w := range weights {
		sum += w
	}

	if sum < 1e-9 {
		return 0.75
	}

	score :=
		hint.ReasoningScore*m.Reasoning +
			hint.CodingScore*m.Coding +
			hint.MathScore*m.Math +
			hint.MultilingualScore*m.Multilingual

	return clamp01(score / sum)
}

// Catalog returns a copy of the default model catalog.
func Catalog() []ModelCard {
	out := make([]ModelCard, len(catalog))
	copy(out, catalog)
	return out
}

// InputShare converts a prompt character count to the estimated fraction of tokens
// that are input (vs. output). Used for blended cost calculations.
func InputShare(promptChars int) float64 {
	return estimateInputShare(promptChars)
}

// Route selects the single best catalog model for the given hint.
// It returns "" when no model satisfies a positive MaxCostUSDPer1M cap.
func Route(hint TaskHint) string {
	picks := scoreAll(hint, catalog)
	if len(picks) == 0 {
		return ""
	}
	return picks[0].Model
}

// scoreAll scores every model in models against hint and returns candidates sorted
// by descending utility (best first). Models that violate the cost cap are excluded.
func scoreAll(hint TaskHint, models []ModelCard) []Candidate {
	inputShare := estimateInputShare(hint.PromptChars)

	costs := make([]float64, len(models))
	latencies := make([]float64, len(models))
	for i, m := range models {
		costs[i] = blendedCost(m, inputShare)
		latencies[i] = m.LatencySec
	}

	minCost, maxCost := minMax(costs)
	minLat, maxLat := minMax(latencies)

	latencyWeight := wLatency
	if hint.LatencySensitive {
		latencyWeight *= 1.5
	}

	var picks []Candidate
	for i, m := range models {
		cost := costs[i]
		if hint.MaxCostUSDPer1M > 0 && cost > hint.MaxCostUSDPer1M {
			continue
		}
		normCost := normalizeMinMax(cost, minCost, maxCost)
		normLat := normalizeMinMax(m.LatencySec, minLat, maxLat)
		capability := capabilityMatch(m, hint)
		utility := wCapability*capability -
			wCost*normCost -
			latencyWeight*normLat -
			wFailure*m.FailureRate
		picks = append(picks, Candidate{Model: m.ID, Score: utility})
	}

	sort.Slice(picks, func(i, j int) bool {
		return picks[i].Score > picks[j].Score
	})
	return picks
}
