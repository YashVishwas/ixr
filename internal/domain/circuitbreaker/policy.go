package circuitbreaker

import "time"

// Policy configures circuit breaker thresholds for a single model.
type Policy struct {
	SuccessRateThreshold float64
	WindowDuration       time.Duration
	MinRequests          int
	HalfOpenAfter        time.Duration
	ProbeCount           int
}

// DefaultPolicy trips at < 90% success over 2-minute windows; probes after 30s.
var DefaultPolicy = Policy{
	SuccessRateThreshold: 0.90,
	WindowDuration:       2 * time.Minute,
	MinRequests:          10,
	HalfOpenAfter:        30 * time.Second,
	ProbeCount:           3,
}
