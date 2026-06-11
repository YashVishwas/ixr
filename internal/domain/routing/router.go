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
}

var catalog = []ModelCard{
	{
		ID: "claude-sonnet-4-6",

		InputUSDPer1M:  3.00,
		OutputUSDPer1M: 15.00,

		LatencySec:  1.2,
		FailureRate: 0.02,

		Reasoning:    0.95,
		Coding:       0.92,
		Math:         0.95,
		Multilingual: 0.90,
	},
	{
		ID: "gpt-4o-mini",

		InputUSDPer1M:  0.15,
		OutputUSDPer1M: 0.60,

		LatencySec:  0.5,
		FailureRate: 0.025,

		Reasoning:    0.85,
		Coding:       0.88,
		Math:         0.87,
		Multilingual: 0.82,
	},
	{
		ID: "gemini-1.5-flash",

		InputUSDPer1M:  0.075,
		OutputUSDPer1M: 0.30,

		LatencySec:  0.6,
		FailureRate: 0.025,

		Reasoning:    0.84,
		Coding:       0.82,
		Math:         0.86,
		Multilingual: 0.92,
	},
	{
		ID: "deepseek-chat",

		InputUSDPer1M:  0.27,
		OutputUSDPer1M: 1.10,

		LatencySec:  2.0,
		FailureRate: 0.035,

		Reasoning:    0.83,
		Coding:       0.85,
		Math:         0.88,
		Multilingual: 0.76,
	},
	{
		ID: "llama-3.3-70b-versatile",

		InputUSDPer1M:  0,
		OutputUSDPer1M: 0,

		LatencySec:  0.8,
		FailureRate: 0.04,

		Reasoning:    0.80,
		Coding:       0.76,
		Math:         0.80,
		Multilingual: 0.76,
	},
	{
		ID: "gpt-oss-120b",

		InputUSDPer1M:  0,
		OutputUSDPer1M: 0,

		LatencySec:  0.5,
		FailureRate: 0.04,

		Reasoning:    0.74,
		Coding:       0.72,
		Math:         0.75,
		Multilingual: 0.72,
	},
	{
		ID: "zai-glm-4.7",

		InputUSDPer1M:  0,
		OutputUSDPer1M: 0,

		LatencySec:  0.4,
		FailureRate: 0.045,

		Reasoning:    0.68,
		Coding:       0.65,
		Math:         0.68,
		Multilingual: 0.80,
	},
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

// Route selects the single best catalog model for the given hint.
// It returns "" when no model satisfies a positive MaxCostUSDPer1M cap.
func Route(hint TaskHint) string {
	costs := make([]float64, len(catalog))
	latencies := make([]float64, len(catalog))

	inputShare := estimateInputShare(hint.PromptChars)

	for i, m := range catalog {
		costs[i] = blendedCost(m, inputShare)
		latencies[i] = m.LatencySec
	}

	minCost, maxCost := minMax(costs)
	minLat, maxLat := minMax(latencies)

	latencyWeight := wLatency
	if hint.LatencySensitive {
		latencyWeight *= 1.5
	}

	type scored struct {
		modelID string
		utility float64
	}
	var picks []scored

	for i, m := range catalog {
		cost := costs[i]

		if hint.MaxCostUSDPer1M > 0 && cost > hint.MaxCostUSDPer1M {
			continue
		}

		normCost := normalizeMinMax(cost, minCost, maxCost)
		normLat := normalizeMinMax(m.LatencySec, minLat, maxLat)

		capability := capabilityMatch(m, hint)

		costPenalty := wCost * normCost
		latencyPenalty := latencyWeight * normLat
		failurePenalty := wFailure * m.FailureRate

		utility :=
			wCapability*capability -
				costPenalty -
				latencyPenalty -
				failurePenalty

		picks = append(picks, scored{modelID: m.ID, utility: utility})
	}

	if len(picks) == 0 {
		return ""
	}

	sort.Slice(picks, func(i, j int) bool {
		return picks[i].utility > picks[j].utility
	})

	return picks[0].modelID
}
