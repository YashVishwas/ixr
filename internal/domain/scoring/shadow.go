package scoring

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/schema"
	"github.com/YashVishwas/ixr/pkg/store"
)

// ShadowResult holds the outcome of one shadow model invocation.
type ShadowResult struct {
	Model     string
	Latency   time.Duration
	TokensIn  int
	TokensOut int
	Success   bool
	Error     error
}

// Orchestrator manages shadow routing: the primary model serves the caller while
// shadow models process the same request in background goroutines for comparison.
type Orchestrator struct {
	perfStore store.ModelPerfStore
	weights   RewardWeights
	bandit    Bandit
}

// NewOrchestrator creates a shadow routing orchestrator.
func NewOrchestrator(perf store.ModelPerfStore, weights RewardWeights, bandit Bandit) *Orchestrator {
	return &Orchestrator{perfStore: perf, weights: weights, bandit: bandit}
}

// RunShadow fires one background goroutine per model in shadowModels.
// bgCtx must be a context independent of the caller's request (e.g. context.Background()).
// Results are recorded to perfStore; bandit.Update is called with a simple reward signal.
func (o *Orchestrator) RunShadow(bgCtx context.Context, req *schema.RequestEnvelope, shadowModels []string, lookup routing.ProviderLookup) {
	for _, model := range shadowModels {
		model := model
		go func() {
			shadowReq := deepCopyReq(req)
			shadowReq.Model = model

			p, err := lookup(model)
			if err != nil {
				return
			}

			start := time.Now()
			resp, err := p.Chat(bgCtx, shadowReq)
			latency := time.Since(start)

			success := err == nil
			stats := store.ModelStats{
				Model:        model,
				Intent:       "",
				SuccessRate:  floatBool(success),
				P50LatencyMS: float64(latency.Milliseconds()),
				P95LatencyMS: float64(latency.Milliseconds()),
			}
			_ = o.perfStore.Upsert(bgCtx, stats)

			if o.bandit != nil {
				reward := floatBool(success) * 0.7
				if latency < 2*time.Second {
					reward += 0.3
				}
				// Quality bonus, kept in lockstep with
				// plugins/banditreward's identical formula: both write into
				// the same shared bandit instance (see pkg/ixr.Start), so
				// letting one add a quality term the other doesn't would
				// bias arm statistics between primary and shadow traffic
				// for reasons having nothing to do with actual quality.
				var choices []schema.Choice
				if resp != nil {
					choices = resp.Choices
				}
				reward += o.weights.Delta * QualityFromFinishReason(choices)
				o.bandit.Update(model, reward)
			}
		}()
	}
}

func deepCopyReq(req *schema.RequestEnvelope) *schema.RequestEnvelope {
	b, _ := json.Marshal(req)
	var out schema.RequestEnvelope
	_ = json.Unmarshal(b, &out)
	return &out
}

func floatBool(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
