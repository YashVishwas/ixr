package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/YashVishwas/ixr/pkg/schema"
)

const otelTracerName = "ixr/telemetry"

// OTELSink emits one OpenTelemetry span per CallEvent using the GenAI
// semantic conventions (gen_ai.*). Spans are exported to whatever OTLP
// backend is configured via IXR_OTLP_ENDPOINT — Datadog, Grafana, Honeycomb,
// Jaeger, or any other OTLP-compatible collector.
//
// When no OTLP endpoint is configured the global tracer is a no-op and this
// sink adds zero overhead.
type OTELSink struct{}

// NewOTELSink returns a Sink that emits GenAI semantic convention spans.
func NewOTELSink() *OTELSink { return &OTELSink{} }

// Write creates a completed span for ev using the GenAI semantic conventions.
// The span is started at ev.Timestamp and ended at ev.Timestamp+ev.Latency
// so it accurately represents when the provider call happened, not when the
// plugin processed the event.
func (s *OTELSink) Write(ctx context.Context, rec schema.TelemetryRecord) error {
	// No-op when no tracer provider is configured (local dev without OTLP).
	tp := otel.GetTracerProvider()
	if tp == nil {
		return nil
	}

	tracer := tp.Tracer(otelTracerName)

	// Start the span at the actual call time, not plugin processing time.
	startOpts := []trace.SpanStartOption{
		trace.WithTimestamp(rec.Timestamp),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(genAIAttributes(rec)...),
	}

	_, span := tracer.Start(ctx, "gen_ai.chat", startOpts...)

	if !rec.Success {
		span.SetStatus(codes.Error, "provider returned an error")
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.End(trace.WithTimestamp(rec.Timestamp.Add(time.Duration(rec.LatencyMS) * time.Millisecond)))
	return nil
}

// genAIAttributes builds the GenAI semantic convention attribute set for a record.
func genAIAttributes(rec schema.TelemetryRecord) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.GenAiSystemKey.String(normaliseProvider(rec.Provider)),
		semconv.GenAiRequestModelKey.String(rec.Model),
		semconv.GenAiUsagePromptTokensKey.Int(rec.TokensIn),
		semconv.GenAiUsageCompletionTokensKey.Int(rec.TokensOut),
	}

	if rec.ResponseModel != "" && rec.ResponseModel != rec.Model {
		attrs = append(attrs, semconv.GenAiResponseModelKey.String(rec.ResponseModel))
	}
	if rec.RequestID != "" {
		attrs = append(attrs, semconv.GenAiResponseIDKey.String(rec.RequestID))
	}
	if rec.FinishReason != "" {
		attrs = append(attrs, semconv.GenAiResponseFinishReasonsKey.StringSlice([]string{rec.FinishReason}))
	}
	if rec.MaxTokens > 0 {
		attrs = append(attrs, semconv.GenAiRequestMaxTokensKey.Int(rec.MaxTokens))
	}

	// ixr-specific attributes that go beyond the GenAI conventions.
	if rec.TenantID != "" {
		attrs = append(attrs, attribute.String("ixr.tenant_id", rec.TenantID))
	}
	if rec.UseCaseID != "" {
		attrs = append(attrs, attribute.String("ixr.use_case_id", rec.UseCaseID))
	}
	attrs = append(attrs, attribute.Int("ixr.latency_ms", rec.LatencyMS))
	if rec.CostUSD > 0 {
		attrs = append(attrs, attribute.Float64("ixr.cost_usd", rec.CostUSD))
	}

	return attrs
}

// normaliseProvider maps ixr provider names to the gen_ai.system values
// defined by the OpenTelemetry GenAI semantic conventions.
func normaliseProvider(provider string) string {
	switch provider {
	case "openai", "openaicompat", "cerebras", "groq", "sambanova", "github":
		return "openai" // OpenAI-compatible wire format
	case "anthropic":
		return "anthropic"
	case "gemini", "googleai", "gemma":
		return "vertex_ai" // Google GenAI family
	case "mistral":
		return "mistral_ai"
	case "ollama":
		return "ollama"
	case "deepseek":
		return "deepseek"
	default:
		if provider == "" {
			return "_other"
		}
		return provider
	}
}

