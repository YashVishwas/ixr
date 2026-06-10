package scoring

import (
	"context"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/store"
)

type stubPerfStore struct{}

func (stubPerfStore) Get(_ context.Context, _, _ string) (store.ModelStats, error) {
	return store.ModelStats{}, nil
}
func (stubPerfStore) Upsert(_ context.Context, _ store.ModelStats) error { return nil }
func (stubPerfStore) List(_ context.Context, _ string) ([]store.ModelStats, error) {
	return nil, nil
}

type stubPolicyStore struct{}

func (stubPolicyStore) GetPolicy(_ context.Context, _ string) (store.RoutingPolicy, error) {
	return store.RoutingPolicy{CostWeight: 0.3, LatencyWeight: 0.4, ReliabilityWeight: 0.3}, nil
}
func (stubPolicyStore) SetPolicy(_ context.Context, _ store.RoutingPolicy) error { return nil }

func TestEngineDecideFiltersScoresAndBuildsFallbacks(t *testing.T) {
	cat := []routing.ModelCard{
		{ID: "cheap-fast", InputUSDPer1M: 0.5, OutputUSDPer1M: 0.5, LatencySec: 0.2, FailureRate: 0.01},
		{ID: "slow", InputUSDPer1M: 0.5, OutputUSDPer1M: 0.5, LatencySec: 10.0, FailureRate: 0.01},
		{ID: "fallback", InputUSDPer1M: 1.0, OutputUSDPer1M: 1.0, LatencySec: 0.5, FailureRate: 0.02},
	}
	eng := NewEngine(stubPerfStore{}, stubPolicyStore{}, cat)
	decision, err := eng.Decide(context.Background(), routing.TaskHint{}, nil)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Model == "" {
		t.Fatal("expected a primary model")
	}
	if len(decision.FallbackChain) > 2 {
		t.Fatalf("fallback chain too long: %+v", decision.FallbackChain)
	}
}

func TestEngineDecideErrorsOnEmptyCatalog(t *testing.T) {
	eng := NewEngine(stubPerfStore{}, stubPolicyStore{}, nil)
	_, err := eng.Decide(context.Background(), routing.TaskHint{}, nil)
	if err == nil {
		t.Fatal("expected error for empty catalog")
	}
}
