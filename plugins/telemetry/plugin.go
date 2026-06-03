// Package telemetry converts CallEvents into TelemetryRecords and writes them downstream.
package telemetry

import (
	"context"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Sink persists telemetry records.
type Sink interface {
	Write(ctx context.Context, record schema.TelemetryRecord) error
}

// Plugin is the reference telemetry EventConsumer.
type Plugin struct {
	Sink Sink
}

func (p *Plugin) Name() string { return "telemetry" }

func (p *Plugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	if p == nil || p.Sink == nil || ev == nil {
		return nil
	}
	record := schema.TelemetryRecord{
		RequestID: ev.ID,
		UseCaseID: ev.UseCaseID,
		Model:     ev.Model,
		Provider:  ev.Provider,
		LatencyMS: int(ev.LatencyMS),
		TokensIn:  ev.TokensIn,
		TokensOut: ev.TokensOut,
		CostUSD:   ev.Cost.TotalUSD,
		Success:   ev.Error == "",
		Timestamp: ev.Timestamp,
	}
	if len(ev.Response.Choices) > 0 {
		record.FinishReason = ev.Response.Choices[0].FinishReason
	}
	if ev.Shadow != nil {
		record.Shadow = true
		record.ShadowOf = ev.Shadow.PrimaryID
	}
	return p.Sink.Write(ctx, record)
}
