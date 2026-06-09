package policystore

import "context"

// Weights are deterministic routing weights for one intent.
type Weights struct {
	Cost        float64
	Latency     float64
	Reliability float64
}

// ShadowPolicy configures shadow routing per use case.
type ShadowPolicy struct {
	Enabled bool
	Model   string
}

// Store reads routing policy from hot storage.
type Store interface {
	Weights(ctx context.Context, intent string) (Weights, error)
	Shadow(ctx context.Context, useCaseID string) (ShadowPolicy, error)
}
