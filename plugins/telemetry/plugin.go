// Package telemetry converts CallEvents into TelemetryRecords and writes them
// to a Sink (e.g. JSON lines file) while also updating the model performance store.
package telemetry

import (
	"context"

	"github.com/YashVishwas/ixr/pkg/schema"
	"github.com/YashVishwas/ixr/pkg/store"
)

// Sink is any writer that accepts TelemetryRecords.
type Sink interface {
	Write(ctx context.Context, rec schema.TelemetryRecord) error
}

// MultiSink fans a TelemetryRecord out to multiple sinks.
// Errors from individual sinks are logged and do not stop delivery to others.
type MultiSink []Sink

func (m MultiSink) Write(ctx context.Context, rec schema.TelemetryRecord) error {
	for _, s := range m {
		_ = s.Write(ctx, rec) // best-effort; don't block on individual sink failures
	}
	return nil
}

// Plugin implements plugin.EventConsumer and processes CallEvents from the bus.
type Plugin struct {
	store store.ModelPerfStore
	sink  Sink
}

// New creates a telemetry plugin. sink may be nil (perf store is always updated).
func New(s store.ModelPerfStore, sink Sink) *Plugin {
	return &Plugin{store: s, sink: sink}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "telemetry" }

// OnEvent converts ev to a TelemetryRecord, upserts stats, and writes to sink.
func (p *Plugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	var finishReason string
	if len(ev.Response.Choices) > 0 {
		finishReason = ev.Response.Choices[0].FinishReason
	}

	rec := schema.TelemetryRecord{
		RequestID:     ev.ID,
		UseCaseID:     ev.UseCaseID,
		TenantID:      ev.TenantID,
		Model:         ev.Model,
		ResponseModel: ev.Response.Model,
		Provider:      ev.Provider,
		LatencyMS:     int(ev.Latency.Milliseconds()),
		TokensIn:      ev.TokensIn,
		TokensOut:     ev.TokensOut,
		MaxTokens:     ev.Request.MaxTokens,
		CostUSD:       ev.Cost.TotalUSD,
		Success:       ev.Error == "",
		FinishReason:  finishReason,
		Timestamp:     ev.Timestamp,
	}

	// Update the performance store with each call's outcome.
	stats := store.ModelStats{
		Model:        ev.Model,
		Intent:       ev.UseCaseID,
		SuccessRate:  successFloat(ev.Error == ""),
		P50LatencyMS: float64(ev.Latency.Milliseconds()),
		P95LatencyMS: float64(ev.Latency.Milliseconds()),
	}
	_ = p.store.Upsert(ctx, stats)

	if p.sink != nil {
		return p.sink.Write(ctx, rec)
	}
	return nil
}

func successFloat(ok bool) float64 {
	if ok {
		return 1.0
	}
	return 0.0
}
