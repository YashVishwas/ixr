package observability

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "ixr"

// InitTracer configures the global OTEL tracer.
// endpoint is the OTLP HTTP collector (e.g. "localhost:4318").
// Pass "" to use a no-op tracer (default, safe for local dev).
// Call the returned shutdown func on process exit.
func InitTracer(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	if endpoint == "" {
		// No-op: leaves the global tracer as the default no-op provider.
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns the global ixr tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// TraceMiddleware wraps each HTTP request in an OTEL span.
func TraceMiddleware(next http.Handler) http.Handler {
	tracer := otel.Tracer(tracerName)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), r.Method+" "+r.URL.Path,
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.String("http.request_id", r.Header.Get("X-Request-ID")),
			),
		)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StartProviderSpan creates a child span for a provider call.
func StartProviderSpan(ctx context.Context, provider, model string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "provider.chat",
		trace.WithAttributes(
			attribute.String("ixr.provider", provider),
			attribute.String("ixr.model", model),
		),
	)
}
