package intent

import "strings"

// ComplexityBucket classifies a request's complexity as a routing signal.
// v1 uses a heuristic: prompt length + presence of code blocks + chain-of-thought markers.
const (
	ComplexityLow    = "low"
	ComplexityMedium = "medium"
	ComplexityHigh   = "high"
)

// ClassifyComplexity applies the v1 heuristic from the PRD.
func ClassifyComplexity(prompt string) string {
	chars := len(prompt)
	score := 0
	if chars > 2000 {
		score++
	}
	if chars > 8000 {
		score++
	}

	lower := strings.ToLower(prompt)
	if strings.Contains(prompt, "```") || strings.Contains(lower, "func ") || strings.Contains(lower, "class ") {
		score++
	}
	if strings.Contains(lower, "step by step") || strings.Contains(lower, "reason") || strings.Contains(lower, "prove") {
		score++
	}

	switch {
	case score >= 3:
		return ComplexityHigh
	case score >= 1:
		return ComplexityMedium
	default:
		return ComplexityLow
	}
}