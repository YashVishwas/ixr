// Package scoring contains the adaptive scoring engine.
// v1: deterministic weighted scoring driven by per-intent weights from the policy store.
// v2: bandit algorithms (epsilon-greedy and UCB) that learn from real traffic.
package scoring

import (
	"context"
	"fmt"
	"sort"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/store"
)

// Engine implements the Phase 2 deterministic scoring pipeline.
// It loads per-intent routing policies, filters the model catalog, queries live
// performance stats, scores each candidate, and returns a RoutingDecision.
type Engine struct {
	perfStore   store.ModelPerfStore
	policyStore store.PolicyStore
	catalog     []routing.ModelCard
}

// NewEngine creates a scoring engine backed by the given stores and model catalog.
// Pass routing.Catalog() for the default catalog.
func NewEngine(perf store.ModelPerfStore, policy store.PolicyStore, catalog []routing.ModelCard) *Engine {
	return &Engine{perfStore: perf, policyStore: policy, catalog: catalog}
}

// Decide selects the best model for hint, using live stats and policy weights.
// cb may be nil (circuit breaker filtering is skipped when nil).
func (e *Engine) Decide(ctx context.Context, hint routing.TaskHint, cb *circuitbreaker.Registry) (routing.RoutingDecision, error) {
	intent := intentFromHint(hint)

	policy, err := e.policyStore.GetPolicy(ctx, intent)
	if err != nil {
		return routing.RoutingDecision{}, fmt.Errorf("scoring engine: get policy: %w", err)
	}

	filtered := routing.Filter(e.catalog, hint, cb)
	if len(filtered) == 0 {
		return routing.RoutingDecision{}, fmt.Errorf("scoring engine: no models pass filter constraints")
	}

	inputShare := routing.InputShare(hint.PromptChars)
	weights := [3]float64{policy.CostWeight, policy.LatencyWeight, policy.ReliabilityWeight}

	candidates := make([]routing.Candidate, 0, len(filtered))
	for _, m := range filtered {
		stats, _ := e.perfStore.Get(ctx, m.ID, intent)
		score := routing.Score(m, hint, stats, weights, inputShare)
		candidates = append(candidates, routing.Candidate{Model: m.ID, Score: score})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	primary := candidates[0].Model
	return routing.RoutingDecision{
		Model:         primary,
		FallbackChain: routing.BuildFallbackChain(candidates, primary, 2),
	}, nil
}

func intentFromHint(hint routing.TaskHint) string {
	switch {
	case hint.ReasoningScore > 0:
		return "reasoning"
	case hint.CodingScore > 0:
		return "coding"
	case hint.MathScore > 0:
		return "math"
	case hint.MultilingualScore > 0:
		return "multilingual"
	default:
		return "general"
	}
}
