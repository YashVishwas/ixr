// Package banditreward closes the loop for RFC Gap 12 (bandit exploration in
// primary auto-routing): scoring.Engine.SetBandit lets Decide pick a model
// via the bandit instead of the deterministic top score, but the bandit only
// improves if something reports back how each pick actually performed. This
// plugin is that something — it watches the event bus for auto-routed calls
// and calls Bandit.Update with a reward computed from success, latency, and
// a finish-reason-derived quality signal — the same formula
// scoring.Orchestrator (shadow routing) already uses, since both write into
// one shared bandit instance and must stay in sync.
package banditreward

import (
	"context"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Plugin is an EventConsumer that trains the bandit from real primary-routed
// traffic. Explicit model requests (AutoRouted == false) are ignored — a
// caller who named a model didn't opt into exploration.
type Plugin struct {
	bandit scoring.Bandit
}

// New creates a reward-recording plugin backed by bandit — the same
// instance passed to scoring.Engine.SetBandit, so exploration and reward
// share one set of arm statistics.
func New(bandit scoring.Bandit) *Plugin {
	return &Plugin{bandit: bandit}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "bandit-reward" }

func (p *Plugin) OnEvent(_ context.Context, ev *schema.CallEvent) error {
	if !ev.AutoRouted || ev.Shadow != nil {
		return nil
	}
	success := ev.Error == ""
	reward := 0.0
	if success {
		reward += 0.7
	}
	if time.Duration(ev.Latency) < 2*time.Second {
		reward += 0.3
	}
	// Quality bonus on top of the success/latency base above — additive
	// rather than folded into the 0.7/0.3 split so bandit.go's
	// failureRewardThreshold (calibrated to that split) keeps classifying
	// success/failure exactly as before: 0 quality contribution when there's
	// no response to read a finish reason from (this plugin's pre-existing
	// behavior for such events) leaves old callers' reward values unchanged.
	reward += scoring.DefaultRewardWeights.Delta * scoring.QualityFromFinishReason(ev.Response.Choices)
	p.bandit.Update(ev.Model, reward)
	return nil
}
