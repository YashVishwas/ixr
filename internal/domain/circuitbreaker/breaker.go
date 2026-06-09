// Package circuitbreaker watches model health and excludes degraded models from routing.
// States: closed (healthy) → open (excluded) → half-open (probe) → closed.
// Circuit state is stored in Redis so all ixr instances see it immediately.
package circuitbreaker

import "time"

// State is the circuit breaker lifecycle state.
type State string

const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half_open"
)

// Stats is the rolling model health snapshot used to evaluate transitions.
type Stats struct {
	Requests    int
	SuccessRate float64
	Window      time.Duration
}

// Breaker is a deterministic state machine for one provider/model circuit.
type Breaker struct {
	policy     Policy
	state      State
	openedAt   time.Time
	halfOpenAt time.Time
}

// New creates a closed circuit breaker.
func New(policy Policy) *Breaker {
	if policy.FailureThreshold == 0 {
		policy = DefaultPolicy()
	}
	return &Breaker{policy: policy, state: Closed}
}

// State returns the current circuit state.
func (b *Breaker) State() State {
	if b == nil {
		return Closed
	}
	return b.state
}

// Allow reports whether a request may pass through the circuit.
func (b *Breaker) Allow(now time.Time) bool {
	if b == nil {
		return true
	}
	if b.state == Open && now.Sub(b.openedAt) >= b.policy.HalfOpenAfter {
		b.state = HalfOpen
		b.halfOpenAt = now
		return true
	}
	return b.state != Open
}

// ObserveHealth updates the circuit from rolling aggregate stats.
func (b *Breaker) ObserveHealth(now time.Time, stats Stats) State {
	if b == nil {
		return Closed
	}
	if b.state != Closed {
		return b.state
	}
	if stats.Requests < b.policy.MinRequests || stats.Window < b.policy.OpenAfter {
		return b.state
	}
	if stats.SuccessRate < b.policy.FailureThreshold {
		b.state = Open
		b.openedAt = now
	}
	return b.state
}

// ProbeSucceeded closes a half-open circuit after a successful probe.
func (b *Breaker) ProbeSucceeded() {
	if b != nil && b.state == HalfOpen {
		b.state = Closed
	}
}

// ProbeFailed reopens a half-open circuit after a failed probe.
func (b *Breaker) ProbeFailed(now time.Time) {
	if b != nil && b.state == HalfOpen {
		b.state = Open
		b.openedAt = now
	}
}
