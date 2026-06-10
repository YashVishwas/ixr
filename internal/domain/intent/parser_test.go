package intent

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestParseHeadersAndRequest(t *testing.T) {
	maxLatency := 1500
	parsed := Parse(Headers{
		"X-IXR-Intent":      {"reasoning"},
		"X-IXR-Max-Cost":    {"0.01"},
		"X-IXR-Max-Latency": {"1500"},
		"X-IXR-Quality":     {"0.9"},
	}, &schema.RequestEnvelope{
		Messages: []schema.Message{{Role: "user", Content: "prove this step by step"}},
	})
	if parsed.Intent != IntentReasoning {
		t.Fatalf("intent: got %q", parsed.Intent)
	}
	if parsed.MaxCostUSD == nil || *parsed.MaxCostUSD != 0.01 {
		t.Fatalf("max cost: got %v", parsed.MaxCostUSD)
	}
	if parsed.MaxLatencyMS == nil || *parsed.MaxLatencyMS != maxLatency {
		t.Fatalf("max latency: got %v", parsed.MaxLatencyMS)
	}
	if parsed.MinQuality == nil || *parsed.MinQuality != 0.9 {
		t.Fatalf("quality: got %v", parsed.MinQuality)
	}
	if parsed.ComplexityBucket != ComplexityMedium {
		t.Fatalf("complexity: got %q", parsed.ComplexityBucket)
	}
}

func TestClassifyComplexityHigh(t *testing.T) {
	prompt := "reason step by step\n```go\nfunc main() {}\n```" + string(make([]byte, 9000))
	if got := ClassifyComplexity(prompt); got != ComplexityHigh {
		t.Fatalf("complexity: got %q, want high", got)
	}
}