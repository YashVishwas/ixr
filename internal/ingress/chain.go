package ingress

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/cost"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/reasoning"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// handleChain runs c's models sequentially — each step's assistant reply
// feeds into the next step's prompt as the RFC's fast-refine/debate
// patterns intend — and returns the final step's response to the caller.
// Each step publishes its own CallEvent tagged with its real model, so
// per-step cost/latency stay observable through the existing telemetry and
// audit-log path without any new schema. Intermediate steps are not
// streamed or exposed to the client in v1; a step failure aborts the chain
// rather than skipping ahead, since a later step reasoning over a missing
// prior turn would produce a confusing result.
func (h *ChatHandler) handleChain(w http.ResponseWriter, r *http.Request, c chain.Chain, req *schema.RequestEnvelope) {
	id := identity.FromContext(r.Context())
	base := append([]schema.Message(nil), req.Messages...)

	var resp *schema.ResponseEnvelope
	for i, model := range c.Models {
		stepMessages := append([]schema.Message(nil), base...)
		if i > 0 {
			stepMessages = append(stepMessages, schema.Message{Role: "assistant", Content: resp.Choices[0].Message.Content})
		}
		if c.Prompts[i] != "" {
			stepMessages = append(stepMessages, schema.Message{Role: "user", Content: c.Prompts[i]})
		}
		stepReq := &schema.RequestEnvelope{Model: model, Messages: stepMessages}

		start := time.Now()
		result, err := routing.Execute(r.Context(), routing.RoutingDecision{Model: model}, reasoning.AdjustTokenBudget(stepReq), routing.ProviderLookup(h.router), h.retryCfg)
		latency := time.Since(start)

		if h.bus != nil {
			ev := &schema.CallEvent{
				Timestamp: start,
				Model:     model,
				Latency:   schema.EventLatency(latency),
				Request:   *stepReq,
				UseCaseID: r.Header.Get("X-IXR-UseCase"),
				TenantID:  id.TenantID,
				TeamID:    id.TeamID,
				UserID:    id.UserID,
			}
			if result.Provider != nil {
				ev.Provider = result.Provider.Name()
			}
			if err != nil {
				ev.Error = err.Error()
			} else {
				ev.ID = result.Response.ID
				ev.TokensIn = result.Response.Usage.PromptTokens
				ev.TokensOut = result.Response.Usage.CompletionTokens
				ev.Response = *result.Response
				ev.Cost = cost.ForUsage(model, ev.TokensIn, ev.TokensOut)
			}
			if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
				slog.Warn("bus publish error (chain step)", "err", pubErr)
			}
		}

		if err != nil {
			slog.Error("chain step failed", "chain", c.Name, "step", i, "model", model, "err", err)
			writeError(w, http.StatusBadGateway, "chain_step_failed", "chain step for model "+model+" failed: upstream provider returned an error")
			return
		}
		resp = result.Response
	}

	resp.Model = c.Name
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}
