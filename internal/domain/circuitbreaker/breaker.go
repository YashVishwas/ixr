// Package circuitbreaker watches model health and excludes degraded models from routing.
// States: closed (healthy) → open (excluded) → half-open (probe) → closed.
package circuitbreaker

import (
	"sync"
	"time"
)

// State is the circuit breaker state.
type State int

const (
	StateClosed   State = iota // healthy; all requests pass
	StateOpen                  // tripped; requests rejected
	StateHalfOpen              // probing; limited requests allowed
)

// Breaker is a per-model circuit breaker implementing a sliding-window state machine.
type Breaker struct {
	mu           sync.Mutex
	state        State
	policy       Policy
	windowStart  time.Time
	failures     int
	total        int
	openedAt     time.Time
	probesSent   int
	probesPassed int
}

// New creates a closed Breaker using the given policy.
func New(p Policy) *Breaker {
	return &Breaker{
		state:       StateClosed,
		policy:      p,
		windowStart: time.Now(),
	}
}

// Allow returns true if a request should be forwarded to the upstream provider.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	switch b.state {
	case StateClosed:
		if now.Sub(b.windowStart) > b.policy.WindowDuration {
			b.failures, b.total = 0, 0
			b.windowStart = now
		}
		return true
	case StateOpen:
		if now.Sub(b.openedAt) < b.policy.HalfOpenAfter {
			return false
		}
		b.state = StateHalfOpen
		b.probesSent, b.probesPassed = 0, 0
		fallthrough
	case StateHalfOpen:
		if b.probesSent < b.policy.ProbeCount {
			b.probesSent++
			return true
		}
		return false
	}
	return false
}

// RecordSuccess must be called after a successful provider response.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		b.total++
	case StateHalfOpen:
		b.probesPassed++
		if b.probesPassed >= b.policy.ProbeCount {
			b.state = StateClosed
			b.failures, b.total = 0, 0
			b.windowStart = time.Now()
		}
	}
}

// RecordFailure must be called after a provider error.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		b.failures++
		b.total++
		if b.total >= b.policy.MinRequests {
			rate := float64(b.total-b.failures) / float64(b.total)
			if rate < b.policy.SuccessRateThreshold {
				b.state = StateOpen
				b.openedAt = time.Now()
			}
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = time.Now()
	}
}

// CurrentState returns the breaker's current state (for monitoring).
func (b *Breaker) CurrentState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// setState seeds the breaker's state from an external source (a shared
// StateStore — see registry.go) rather than a local Allow/RecordSuccess/
// RecordFailure transition. Only used when creating a Breaker that a
// StateStore says another ixr instance already tripped, so a freshly
// created local Breaker doesn't start Closed and blindly send traffic to a
// model every other replica has already learned is unhealthy.
//
// openedAt is set to now rather than preserved from whatever instance
// originally tripped it — this instance doesn't know the original trip
// time, only that it's currently Open. The tradeoff: a newly-seeded
// replica always waits the full HalfOpenAfter window before probing, even
// if the model already recovered elsewhere moments ago. Errs toward extra
// caution rather than under-caution, which is the right side to be wrong
// on for a circuit breaker.
func (b *Breaker) setState(s State) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = s
	if s == StateOpen {
		b.openedAt = time.Now()
	}
}
