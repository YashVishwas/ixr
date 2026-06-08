package circuitbreaker

import "time"

// Policy holds circuit breaker thresholds and probe logic.
// Defaults: open when success_rate < 0.90 over 2 min; half-open after 30s.
// All thresholds are configurable per provider.
type Policy struct {
	FailureThreshold float64
	MinRequests      int
	OpenAfter        time.Duration
	HalfOpenAfter    time.Duration
}

// DefaultPolicy returns the PRD defaults.
func DefaultPolicy() Policy {
	return Policy{
		FailureThreshold: 0.90,
		MinRequests:      10,
		OpenAfter:        2 * time.Minute,
		HalfOpenAfter:    30 * time.Second,
	}
}