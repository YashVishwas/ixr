package store

import "context"

// RoutingPolicy holds per-intent scoring weights and routing constraints.
type RoutingPolicy struct {
	Intent            string
	CostWeight        float64
	LatencyWeight     float64
	ReliabilityWeight float64
	MaxCostUSD        float64  // 0 = unconstrained
	MaxLatencyMS      int      // 0 = unconstrained
	ShadowEnabled     bool
	ShadowModels      []string
}

// PolicyStore retrieves and stores routing policies keyed by intent.
type PolicyStore interface {
	GetPolicy(ctx context.Context, intent string) (RoutingPolicy, error)
	SetPolicy(ctx context.Context, policy RoutingPolicy) error
}
