// Package intent parses and enriches intent + constraints from the request.
// It maps X-IXR-* headers and x_ixr body fields into a ParsedRequest that
// the routing engine uses to pick a model.
package intent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// ParsedRequest is the enriched representation of an inbound request
// after intent and constraint extraction.
type ParsedRequest struct {
	Intent           string   // one of the taxonomy constants
	MaxCostUSD       *float64 // hard ceiling; nil = unconstrained
	MaxLatencyMS     *int     // hard ceiling; nil = unconstrained
	MinQuality       *float64 // 0.0–1.0; nil = unconstrained
	TokenEstimate    int      // derived from prompt token count
	ComplexityBucket string   // "low" | "medium" | "high" (heuristic)
}

// Headers is the subset of http.Header consumed by the parser.
type Headers map[string][]string

// Parse extracts intent and constraints from headers and a canonical request.
func Parse(headers Headers, req *schema.RequestEnvelope) ParsedRequest {
	prompt := promptText(req)
	parsed := ParsedRequest{
		Intent:           normalizeIntent(first(headers, "X-IXR-Intent")),
		TokenEstimate:    estimateTokens(prompt),
		ComplexityBucket: ClassifyComplexity(prompt),
	}
	if parsed.Intent == "" {
		parsed.Intent = normalizeIntent(first(headers, "X-IXR-Task"))
	}
	if v, ok := parseFloat(first(headers, "X-IXR-Max-Cost")); ok {
		parsed.MaxCostUSD = &v
	} else if v, ok := parseFloat(first(headers, "X-IXR-Budget")); ok {
		parsed.MaxCostUSD = &v
	}
	if v, ok := parseInt(first(headers, "X-IXR-Max-Latency")); ok {
		parsed.MaxLatencyMS = &v
	}
	if v, ok := parseFloat(first(headers, "X-IXR-Quality")); ok {
		parsed.MinQuality = &v
	}
	return parsed
}

func promptText(req *schema.RequestEnvelope) string {
	if req == nil {
		return ""
	}
	var b strings.Builder
	for _, msg := range req.Messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
		for _, tc := range msg.ToolCalls {
			b.WriteString(tc.Function.Name)
			b.WriteByte('\n')
			b.WriteString(tc.Function.Arguments)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

func first(headers Headers, key string) string {
	for k, values := range headers {
		if !strings.EqualFold(k, key) {
			continue
		}
		for _, v := range values {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func parseFloat(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	return v, err == nil
}

func parseInt(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	return v, err == nil
}

func normalizeIntent(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case IntentReasoning, "coding", "math":
		return IntentReasoning
	case IntentSummarization, "summary":
		return IntentSummarization
	case IntentExtraction, "extract":
		return IntentExtraction
	case IntentGeneration, "creative":
		return IntentGeneration
	case IntentClassification, "classify":
		return IntentClassification
	case IntentEmbedding, "embeddings":
		return IntentEmbedding
	default:
		var body struct {
			Intent string `json:"intent"`
		}
		if json.Unmarshal([]byte(raw), &body) == nil && body.Intent != "" {
			return normalizeIntent(body.Intent)
		}
		return ""
	}
}