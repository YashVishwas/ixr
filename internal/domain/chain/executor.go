// Package chain implements synchronous multi-model chaining.
// Each stage receives the full conversation history (original messages plus
// every prior stage's assistant reply), so later models can address gaps
// or uncertainties raised by earlier ones.
package chain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Stage is one step in a chain: a resolved model name and an optional system
// message that is prepended to the conversation only for that step.
type Stage struct {
	Model  string // model name or "auto"
	Prompt string // injected system message; empty means no injection
}

// Executor runs multi-stage chains synchronously.
// Each stage publishes its own CallEvent to the bus so the audit log shows
// every hop with chain.id / chain.stage / chain.total_stages metadata.
type Executor struct {
	router  func(string) (provider.Provider, error)
	resolve func(routing.TaskHint) string // wraps routing.Route
	bus     bus.Bus
}

// NewExecutor creates a chain executor.
// router maps a model name to its provider (same func used by the ingress handler).
// resolve maps a TaskHint to a model name (pass routing.Route).
func NewExecutor(
	router func(string) (provider.Provider, error),
	resolve func(routing.TaskHint) string,
	b bus.Bus,
) *Executor {
	return &Executor{router: router, resolve: resolve, bus: b}
}

// Run executes stages sequentially, returning the final stage's response.
// chainID is a unique ID for this chain invocation (caller-supplied so it also
// appears in the HTTP response as the top-level response ID).
// chainName is the named chain key from config, or the raw header value.
// hint is the TaskHint derived from request headers, used to resolve "auto".
func (e *Executor) Run(
	ctx context.Context,
	chainID, chainName string,
	stages []Stage,
	req *schema.RequestEnvelope,
	useCaseID string,
	hint routing.TaskHint,
) (*schema.ResponseEnvelope, error) {
	if len(stages) == 0 {
		return nil, fmt.Errorf("chain: no stages defined")
	}

	// Build a mutable copy of the conversation history that grows with each stage.
	msgs := make([]schema.Message, len(req.Messages))
	copy(msgs, req.Messages)

	var lastResp *schema.ResponseEnvelope

	for i, stage := range stages {
		model := stage.Model
		if model == "auto" {
			model = e.resolve(hint)
			if model == "" {
				return nil, fmt.Errorf("chain stage %d: auto-routing returned no model", i+1)
			}
		}

		prov, err := e.router(model)
		if err != nil {
			return nil, fmt.Errorf("chain stage %d (%s): %w", i+1, model, err)
		}

		// Build the messages for this stage: shared history + optional injected prompt.
		stageMsgs := msgs
		if stage.Prompt != "" {
			stageMsgs = append([]schema.Message{{Role: "system", Content: stage.Prompt}}, msgs...)
		}

		stageReq := schema.RequestEnvelope{
			Model:    model,
			Messages: stageMsgs,
		}

		t0 := time.Now()
		resp, callErr := prov.Chat(ctx, &stageReq)
		latency := time.Since(t0)

		// Publish a CallEvent for this stage regardless of error.
		if e.bus != nil {
			ev := &schema.CallEvent{
				Timestamp: t0,
				Provider:  prov.Name(),
				Model:     model,
				LatencyMS: latency.Milliseconds(),
				Request:   stageReq,
				UseCaseID: useCaseID,
				Chain: &schema.ChainMeta{
					ID:          chainID,
					Name:        chainName,
					Stage:       i + 1,
					TotalStages: len(stages),
				},
			}
			if callErr != nil {
				ev.Error = callErr.Error()
			} else {
				ev.ID = resp.ID
				ev.TokensIn = resp.Usage.PromptTokens
				ev.TokensOut = resp.Usage.CompletionTokens
				ev.Response = *resp
			}
			if pubErr := e.bus.Publish(ctx, ev); pubErr != nil {
				slog.Warn("chain: bus publish error", "stage", i+1, "err", pubErr)
			}
		}

		if callErr != nil {
			return nil, fmt.Errorf("chain stage %d (%s): %w", i+1, model, callErr)
		}

		// Append this stage's response to the shared history so the next stage
		// can see what the previous model said.
		if len(resp.Choices) > 0 {
			msgs = append(msgs, schema.Message{
				Role:    resp.Choices[0].Message.Role,
				Content: resp.Choices[0].Message.Content,
			})
		}

		lastResp = resp
	}

	return lastResp, nil
}
