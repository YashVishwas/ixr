package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeRec(provider, model string, tokensIn, tokensOut, latencyMS int, success bool) schema.TelemetryRecord {
	return schema.TelemetryRecord{
		RequestID:     "req-1",
		Provider:      provider,
		Model:         model,
		ResponseModel: model + "-20251001", // providers often version the model name
		TokensIn:      tokensIn,
		TokensOut:     tokensOut,
		MaxTokens:     1000,
		CostUSD:       0.0025,
		LatencyMS:     latencyMS,
		Success:       success,
		TenantID:      "acme",
		UseCaseID:     "summarization",
		Timestamp:     time.Now(),
	}
}

// --- normaliseProvider ---

func TestNormaliseProvider(t *testing.T) {
	cases := []struct{ in, want string }{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"gemini", "vertex_ai"},
		{"googleai", "vertex_ai"},
		{"gemma", "vertex_ai"},
		{"mistral", "mistral_ai"},
		{"ollama", "ollama"},
		{"deepseek", "deepseek"},
		{"cerebras", "openai"},
		{"groq", "openai"},
		{"sambanova", "openai"},
		{"github", "openai"},
		{"unknown-provider", "unknown-provider"},
		{"", "_other"},
	}
	for _, tc := range cases {
		if got := normaliseProvider(tc.in); got != tc.want {
			t.Errorf("normaliseProvider(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- genAIAttributes ---

func TestGenAIAttributes_CoreFields(t *testing.T) {
	rec := makeRec("anthropic", "claude-haiku-4-5", 100, 200, 350, true)
	attrs := genAIAttributes(rec)

	attrMap := make(map[string]interface{})
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value.AsInterface()
	}

	if attrMap["gen_ai.system"] != "anthropic" {
		t.Errorf("gen_ai.system: got %v", attrMap["gen_ai.system"])
	}
	if attrMap["gen_ai.request.model"] != "claude-haiku-4-5" {
		t.Errorf("gen_ai.request.model: got %v", attrMap["gen_ai.request.model"])
	}
	if attrMap["gen_ai.usage.prompt_tokens"] != int64(100) {
		t.Errorf("gen_ai.usage.prompt_tokens: got %v", attrMap["gen_ai.usage.prompt_tokens"])
	}
	if attrMap["gen_ai.usage.completion_tokens"] != int64(200) {
		t.Errorf("gen_ai.usage.completion_tokens: got %v", attrMap["gen_ai.usage.completion_tokens"])
	}
}

func TestGenAIAttributes_IxrExtensions(t *testing.T) {
	rec := makeRec("openai", "gpt-4o", 50, 100, 200, true)
	attrs := genAIAttributes(rec)

	attrMap := make(map[string]interface{})
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value.AsInterface()
	}

	if attrMap["ixr.tenant_id"] != "acme" {
		t.Errorf("ixr.tenant_id: got %v", attrMap["ixr.tenant_id"])
	}
	if attrMap["ixr.use_case_id"] != "summarization" {
		t.Errorf("ixr.use_case_id: got %v", attrMap["ixr.use_case_id"])
	}
	if attrMap["ixr.latency_ms"] != int64(200) {
		t.Errorf("ixr.latency_ms: got %v", attrMap["ixr.latency_ms"])
	}
	if attrMap["ixr.cost_usd"] != 0.0025 {
		t.Errorf("ixr.cost_usd: got %v", attrMap["ixr.cost_usd"])
	}
}

func TestGenAIAttributes_ResponseModel(t *testing.T) {
	rec := makeRec("anthropic", "claude-haiku-4-5", 10, 20, 100, true)
	// ResponseModel differs from Model — should appear as gen_ai.response.model
	attrs := genAIAttributes(rec)
	attrMap := make(map[string]interface{})
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value.AsInterface()
	}
	if attrMap["gen_ai.response.model"] != "claude-haiku-4-5-20251001" {
		t.Errorf("gen_ai.response.model: got %v", attrMap["gen_ai.response.model"])
	}
}

func TestGenAIAttributes_ResponseModelOmittedWhenSame(t *testing.T) {
	rec := makeRec("openai", "gpt-4o", 10, 20, 100, true)
	rec.ResponseModel = rec.Model // same — should not emit the attribute
	attrs := genAIAttributes(rec)
	for _, a := range attrs {
		if string(a.Key) == "gen_ai.response.model" {
			t.Fatal("gen_ai.response.model should be omitted when same as request model")
		}
	}
}

func TestGenAIAttributes_MaxTokens(t *testing.T) {
	rec := makeRec("openai", "gpt-4o", 10, 20, 100, true)
	attrs := genAIAttributes(rec)
	attrMap := make(map[string]interface{})
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value.AsInterface()
	}
	if attrMap["gen_ai.request.max_tokens"] != int64(1000) {
		t.Errorf("gen_ai.request.max_tokens: got %v", attrMap["gen_ai.request.max_tokens"])
	}
}

func TestGenAIAttributes_MaxTokensOmittedWhenZero(t *testing.T) {
	rec := makeRec("openai", "gpt-4o", 10, 20, 100, true)
	rec.MaxTokens = 0
	attrs := genAIAttributes(rec)
	for _, a := range attrs {
		if string(a.Key) == "gen_ai.request.max_tokens" {
			t.Fatal("gen_ai.request.max_tokens should be omitted when zero")
		}
	}
}

func TestGenAIAttributes_ResponseID(t *testing.T) {
	rec := makeRec("openai", "gpt-4o", 10, 20, 100, true)
	attrs := genAIAttributes(rec)

	for _, a := range attrs {
		if string(a.Key) == "gen_ai.response.id" && a.Value.AsString() == "req-1" {
			return
		}
	}
	t.Fatal("gen_ai.response.id not found in attributes")
}

// --- OTELSink.Write ---

func TestOTELSink_WriteNoOp(t *testing.T) {
	// Without an OTLP endpoint configured the global tracer is a no-op.
	// Write should not panic or error.
	sink := NewOTELSink()
	rec := makeRec("anthropic", "claude-haiku-4-5", 100, 200, 300, true)
	if err := sink.Write(context.Background(), rec); err != nil {
		t.Fatalf("OTELSink.Write returned unexpected error: %v", err)
	}
}

func TestOTELSink_WriteError(t *testing.T) {
	// Failed call should not error — error is set on the span, not returned.
	sink := NewOTELSink()
	rec := makeRec("openai", "gpt-4o", 50, 0, 100, false)
	if err := sink.Write(context.Background(), rec); err != nil {
		t.Fatalf("OTELSink.Write should not return error even on failed calls: %v", err)
	}
}

// --- MultiSink ---

func TestMultiSink_FansOut(t *testing.T) {
	var a, b int
	sinkA := &countSink{&a}
	sinkB := &countSink{&b}
	multi := MultiSink{sinkA, sinkB}

	rec := makeRec("anthropic", "claude-haiku-4-5", 10, 20, 50, true)
	_ = multi.Write(context.Background(), rec)

	if a != 1 || b != 1 {
		t.Fatalf("expected both sinks to receive one record: a=%d b=%d", a, b)
	}
}

type countSink struct{ n *int }

func (c *countSink) Write(_ context.Context, _ schema.TelemetryRecord) error {
	*c.n++
	return nil
}
