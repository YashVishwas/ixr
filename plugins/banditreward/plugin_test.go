package banditreward

import (
	"context"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/pkg/schema"
)

type recordingBandit struct {
	calls []struct {
		model  string
		reward float64
	}
}

func (r *recordingBandit) Select(candidates []routing.Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Model
}

func (r *recordingBandit) Update(model string, reward float64) {
	r.calls = append(r.calls, struct {
		model  string
		reward float64
	}{model, reward})
}

func (r *recordingBandit) Regret() *scoring.RegretTracker { return nil }

func TestPlugin_IgnoresExplicitModelRequests(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{Model: "gpt-4o", AutoRouted: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 0 {
		t.Errorf("expected no bandit update for an explicit model request, got %+v", b.calls)
	}
}

func TestPlugin_IgnoresShadowEvents(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Shadow:     &schema.ShadowMetadata{ShadowModel: "gpt-4o"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 0 {
		t.Errorf("expected no bandit update for a shadow event (that's the orchestrator's job), got %+v", b.calls)
	}
}

func TestPlugin_RecordsHighRewardForFastSuccess(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Latency:    schema.EventLatency(500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0].model != "gpt-4o" || b.calls[0].reward != 1.0 {
		t.Fatalf("expected one update(gpt-4o, 1.0), got %+v", b.calls)
	}
}

func TestPlugin_RecordsLowerRewardForSlowSuccess(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Latency:    schema.EventLatency(5 * time.Second),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0].reward != 0.7 {
		t.Fatalf("expected reward 0.7 (success but slow), got %+v", b.calls)
	}
}

// A failed call gets no success reward, but the latency bonus is
// unconditional (matching scoring.Orchestrator's identical formula for
// shadow routing) — a fast failure still isn't scored the same as a slow
// success, since 0.3 < 0.7.
func TestPlugin_RecordsPartialRewardForFastFailure(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Error:      "upstream error",
		Latency:    schema.EventLatency(100 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0].reward != 0.3 {
		t.Fatalf("expected reward 0.3 (no success bonus, but the unconditional latency bonus applies), got %+v", b.calls)
	}
}

func TestPlugin_RecordsZeroRewardForSlowFailure(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Error:      "upstream error",
		Latency:    schema.EventLatency(5 * time.Second),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0].reward != 0.0 {
		t.Fatalf("expected reward 0.0 for a slow failure (neither bonus applies), got %+v", b.calls)
	}
}

// TestPlugin_QualityBonusRewardsCleanCompletion is the regression test for
// the gap this closes: the bandit used to be blind to answer quality
// entirely. A fast success with FinishReason "stop" should score higher
// than an otherwise-identical fast success with no response data at all
// (TestPlugin_RecordsHighRewardForFastSuccess, reward 1.0) — proving the
// finish-reason-derived quality term actually reaches the bandit.
func TestPlugin_QualityBonusRewardsCleanCompletion(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Latency:    schema.EventLatency(500 * time.Millisecond),
		Response: schema.ResponseEnvelope{
			Choices: []schema.Choice{{FinishReason: "stop"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = 1.0 + 0.1 // success(0.7) + fast(0.3) + quality(Delta=0.1 * 1.0)
	if len(b.calls) != 1 || b.calls[0].reward != want {
		t.Fatalf("expected reward %.2f (fast success with clean completion), got %+v", want, b.calls)
	}
}

// TestPlugin_QualityBonusPenalizesTruncation confirms a truncated
// completion (FinishReason "length") scores a smaller quality bonus than a
// clean one, even with identical success/latency.
func TestPlugin_QualityBonusPenalizesTruncation(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Latency:    schema.EventLatency(500 * time.Millisecond),
		Response: schema.ResponseEnvelope{
			Choices: []schema.Choice{{FinishReason: "length"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = 1.0 + 0.03 // success(0.7) + fast(0.3) + quality(Delta=0.1 * 0.3)
	if len(b.calls) != 1 || b.calls[0].reward != want {
		t.Fatalf("expected reward %.2f (fast success but truncated), got %+v", want, b.calls)
	}
}

// TestPlugin_NoResponseDataLeavesRewardUnchanged guards backward
// compatibility: an event with no Response.Choices at all (this plugin's
// entire test suite before this feature, and any caller that doesn't
// populate it) must still score exactly the pre-existing 0.7+0.3 formula,
// not some guessed-at quality default.
func TestPlugin_NoResponseDataLeavesRewardUnchanged(t *testing.T) {
	b := &recordingBandit{}
	p := New(b)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		Model:      "gpt-4o",
		AutoRouted: true,
		Latency:    schema.EventLatency(500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.calls) != 1 || b.calls[0].reward != 1.0 {
		t.Fatalf("expected reward 1.0 unchanged with no response data, got %+v", b.calls)
	}
}
