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

			var choices []schema.Choice
			if resp != nil {
				choices = resp.Choices
			}
			o.Record(bgCtx, model, err == nil, latency, choices)
		}()
	}
}

// Record feeds one shadow-call outcome into perfStore and the bandit. It's
// exposed separately from RunShadow so a caller that already runs its own
// goroutine and needs to publish a CallEvent for the call (see
// ingress.ChatHandler's header-triggered shadow path, which reports cost and
// audit-log data RunShadow itself never had a reason to produce) can still
// feed the one learning path RunShadow uses internally, instead of
// duplicating the reward math and drifting out of sync with it.
func (o *Orchestrator) Record(ctx context.Context, model string, success bool, latency time.Duration, choices []schema.Choice) {
	stats := store.ModelStats{
		Model:        model,
		SuccessRate:  floatBool(success),
		P50LatencyMS: float64(latency.Milliseconds()),
		P95LatencyMS: float64(latency.Milliseconds()),
	}
	_ = o.perfStore.Upsert(ctx, stats)

	if o.bandit == nil {
		return
	}
	reward := floatBool(success) * 0.7
	if latency < 2*time.Second {
		reward += 0.3
	}
	// Quality bonus, kept in lockstep with plugins/banditreward's identical
	// formula: both write into the same shared bandit instance (see
	// pkg/ixr.Start), so letting one add a quality term the other doesn't
	// would bias arm statistics between primary and shadow traffic for
	// reasons having nothing to do with actual quality.
	reward += o.weights.Delta * QualityFromFinishReason(choices)
	o.bandit.Update(model, reward)
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
