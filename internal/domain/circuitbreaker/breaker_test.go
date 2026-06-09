package circuitbreaker

import (
	"testing"
	"time"
)

func TestBreakerOpensAndHalfOpens(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	b := New(DefaultPolicy())

	state := b.ObserveHealth(now, Stats{
		Requests:    20,
		SuccessRate: 0.80,
		Window:      2 * time.Minute,
	})
	if state != Open {
		t.Fatalf("state: got %q, want open", state)
	}
	if b.Allow(now.Add(10 * time.Second)) {
		t.Fatal("open circuit allowed too early")
	}
	if !b.Allow(now.Add(31 * time.Second)) || b.State() != HalfOpen {
		t.Fatalf("state after timeout: got %q, want half_open", b.State())
	}
	b.ProbeSucceeded()
	if b.State() != Closed {
		t.Fatalf("state after probe success: got %q, want closed", b.State())
	}
}

func TestBreakerProbeFailureReopens(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	b := New(DefaultPolicy())
	b.ObserveHealth(now, Stats{Requests: 20, SuccessRate: 0.80, Window: 2 * time.Minute})
	b.Allow(now.Add(31 * time.Second))
	b.ProbeFailed(now.Add(32 * time.Second))
	if b.State() != Open {
		t.Fatalf("state after probe failure: got %q, want open", b.State())
	}
}