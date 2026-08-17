// Package feedback indexes CallEvents by ID so a later caller-submitted
// rating (POST /v1/feedback, internal/ingress/feedback_handler.go) can be
// resolved back to the model that produced the response — the plumbing
// half of strengthening RFC Gap 12's bandit quality signal beyond the
// coarse, free FinishReason proxy plugins/banditreward already uses.
package feedback

import (
	"context"

	domainfeedback "github.com/YashVishwas/ixr/internal/domain/feedback"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Plugin is an EventConsumer that records every non-shadow CallEvent's ID
// into a domainfeedback.Store. Shadow events are skipped — a caller never
// sees a shadow call's ID (only the primary response ID comes back over
// the wire), so indexing them would just be dead weight no feedback
// submission could ever reference.
type Plugin struct {
	store *domainfeedback.Store
}

// New creates an indexing plugin backed by store — the same instance the
// feedback HTTP handler reads from.
func New(store *domainfeedback.Store) *Plugin {
	return &Plugin{store: store}
}

// Name returns the stable plugin identifier.
func (p *Plugin) Name() string { return "feedback-index" }

func (p *Plugin) OnEvent(_ context.Context, ev *schema.CallEvent) error {
	if ev.Shadow != nil {
		return nil
	}
	p.store.Record(ev.ID, domainfeedback.CallInfo{Model: ev.Model, AutoRouted: ev.AutoRouted})
	return nil
}
