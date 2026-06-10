package policystore

import (
	"context"
	"sync"

	"github.com/YashVishwas/ixr/pkg/store"
)

// Memory is the in-memory PolicyStore seeded from DefaultWeights.
// Safe for concurrent use.
type Memory struct {
	mu       sync.RWMutex
	policies map[string]store.RoutingPolicy
}

// NewMemory creates a PolicyStore pre-seeded from a weights map.
// weights maps intent → [costWeight, latencyWeight, reliabilityWeight].
func NewMemory(weights map[string][3]float64) *Memory {
	m := &Memory{policies: make(map[string]store.RoutingPolicy)}
	for intent, w := range weights {
		m.policies[intent] = store.RoutingPolicy{
			Intent:            intent,
			CostWeight:        w[0],
			LatencyWeight:     w[1],
			ReliabilityWeight: w[2],
		}
	}
	return m
}

func (m *Memory) GetPolicy(_ context.Context, intent string) (store.RoutingPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.policies[intent]
	if !ok {
		return store.RoutingPolicy{Intent: intent, CostWeight: 0.3, LatencyWeight: 0.3, ReliabilityWeight: 0.4}, nil
	}
	return p, nil
}

func (m *Memory) SetPolicy(_ context.Context, policy store.RoutingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[policy.Intent] = policy
	return nil
}
