package circuitbreaker

import (
	"testing"
	"time"
)

func shortPolicy() Policy {
	return Policy{
		SuccessRateThreshold: 0.90,
		WindowDuration:       10 * time.Second,
		MinRequests:          5,
		HalfOpenAfter:        10 * time.Millisecond,
		ProbeCount:           2,
	}
}

func TestBreaker_InitiallyClosed(t *testing.T) {
	b := New(shortPolicy())
	if !b.Allow() {
		t.Fatal("new breaker should be closed (allow requests)")
	}
}

func TestBreaker_OpenAfterFailures(t *testing.T) {
	b := New(shortPolicy())
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	if b.CurrentState() != StateOpen {
		t.Fatalf("expected StateOpen after %d failures, got %v", 5, b.CurrentState())
	}
	if b.Allow() {
		t.Fatal("open breaker should deny requests")
	}
}

func TestBreaker_HalfOpenAfterDelay(t *testing.T) {
	p := shortPolicy()
	b := New(p)
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	if b.CurrentState() != StateOpen {
		t.Fatal("expected open")
	}

	time.Sleep(p.HalfOpenAfter + 5*time.Millisecond)
	if !b.Allow() {
		t.Fatal("after HalfOpenAfter delay, first probe should be allowed")
	}
	if b.CurrentState() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %v", b.CurrentState())
	}
}

func TestBreaker_ClosedAfterSuccessfulProbes(t *testing.T) {
	p := shortPolicy()
	b := New(p)

	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	time.Sleep(p.HalfOpenAfter + 5*time.Millisecond)

	for i := 0; i < p.ProbeCount; i++ {
		if !b.Allow() {
			t.Fatalf("probe %d should be allowed", i)
		}
		b.RecordSuccess()
	}
	if b.CurrentState() != StateClosed {
		t.Fatalf("expected StateClosed after %d successful probes, got %v", p.ProbeCount, b.CurrentState())
	}
}

func TestBreaker_ReopenOnProbeFailure(t *testing.T) {
	p := shortPolicy()
	b := New(p)

	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}
	time.Sleep(p.HalfOpenAfter + 5*time.Millisecond)

	b.Allow()
	b.RecordFailure()
	if b.CurrentState() != StateOpen {
		t.Fatalf("expected StateOpen after probe failure, got %v", b.CurrentState())
	}
}

func TestBreaker_NotOpenBelowMinRequests(t *testing.T) {
	p := shortPolicy()
	b := New(p)
	for i := 0; i < p.MinRequests-1; i++ {
		b.RecordFailure()
	}
	if b.CurrentState() != StateClosed {
		t.Fatal("should stay closed when below MinRequests threshold")
	}
}

func TestRegistry_IsAllowed(t *testing.T) {
	r := NewRegistry(shortPolicy())
	if !r.IsAllowed("model-a") {
		t.Fatal("new model should be allowed")
	}
	for i := 0; i < 5; i++ {
		r.RecordOutcome("model-a", false)
	}
	if r.IsAllowed("model-a") {
		t.Fatal("model-a should be blocked after 5 failures")
	}
	if !r.IsAllowed("model-b") {
		t.Fatal("model-b (independent) should still be allowed")
	}
}
