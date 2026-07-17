package telemetry

import (
	"context"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
	"github.com/YashVishwas/ixr/pkg/store"
)

type noopPerfStore struct{}

func (noopPerfStore) Get(_ context.Context, _, _ string) (store.ModelStats, error) {
	return store.ModelStats{}, nil
}
func (noopPerfStore) Upsert(_ context.Context, _ store.ModelStats) error { return nil }
func (noopPerfStore) List(_ context.Context, _ string) ([]store.ModelStats, error) {
	return nil, nil
}

type captureSink struct{ recs []schema.TelemetryRecord }

func (s *captureSink) Write(_ context.Context, rec schema.TelemetryRecord) error {
	s.recs = append(s.recs, rec)
	return nil
}

func TestOnEvent_TagsShadowCalls(t *testing.T) {
	sink := &captureSink{}
	p := New(noopPerfStore{}, sink)

	ev := &schema.CallEvent{
		Model:    "claude-3-5-sonnet",
		Provider: "anthropic",
		Shadow: &schema.ShadowMetadata{
			PrimaryID:    "primary-1",
			PrimaryModel: "gpt-4o",
			ShadowModel:  "claude-3-5-sonnet",
		},
	}
	if err := p.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	if len(sink.recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(sink.recs))
	}
	rec := sink.recs[0]
	if !rec.Shadow {
		t.Error("expected Shadow=true for a shadow-routed event")
	}
	if rec.ShadowOf != "gpt-4o" {
		t.Errorf("ShadowOf: got %q, want gpt-4o", rec.ShadowOf)
	}
}

func TestOnEvent_PrimaryCallNotTaggedShadow(t *testing.T) {
	sink := &captureSink{}
	p := New(noopPerfStore{}, sink)

	ev := &schema.CallEvent{Model: "gpt-4o", Provider: "openai"}
	if err := p.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	rec := sink.recs[0]
	if rec.Shadow {
		t.Error("primary call should not be tagged Shadow=true")
	}
	if rec.ShadowOf != "" {
		t.Errorf("ShadowOf should be empty for a primary call, got %q", rec.ShadowOf)
	}
}

func TestOnEvent_PropagatesFallbackInfo(t *testing.T) {
	sink := &captureSink{}
	p := New(noopPerfStore{}, sink)

	ev := &schema.CallEvent{
		Model:        "llama-4-scout",
		Provider:     "llama",
		FallbackUsed: true,
		FallbackFrom: "gpt-5.3-codex",
	}
	if err := p.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	rec := sink.recs[0]
	if !rec.FallbackUsed {
		t.Error("expected FallbackUsed=true to propagate onto the TelemetryRecord")
	}
	if rec.FallbackFrom != "gpt-5.3-codex" {
		t.Errorf("FallbackFrom: got %q, want gpt-5.3-codex", rec.FallbackFrom)
	}
}
