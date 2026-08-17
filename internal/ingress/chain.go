package ingress

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/cost"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/reasoning"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// errCircuitOpen signals a chain step was blocked by an open circuit
// breaker, as distinct from an upstream provider error.
var errCircuitOpen = errors.New("circuit breaker open")

// handleChain dispatches to the sequential or fusion executor for c.
// Each step/panel member goes through the same observability and
// resilience path a direct request does: a CallEvent tagged with its real
// model, a Prometheus metrics record, and a circuit-breaker check/update —
// a chain is just N requests glued together, not a separate code path that
// happens to skip the usual instrumentation.
func (h *ChatHandler) handleChain(w http.ResponseWriter, r *http.Request, c chain.Chain, req *schema.RequestEnvelope) {
	if c.Strategy == chain.StrategyFusion {
		h.handleFusionChain(w, r, c, req)
		return
	}
	h.handleSequentialChain(w, r, c, req)
}

// handleSequentialChain runs c.Models in order — each step's assistant
// reply feeds into the next step's prompt, per the fast-refine/debate
// patterns in the RFC — and returns the final step's response to the
// caller. A step failure aborts the chain rather than skipping ahead,
// since a later step reasoning over a missing prior turn would produce a
// confusing result. Only the final step honours req.Stream: earlier steps
// need each other's full text to build the next prompt, so they can never
// stream to the client.
func (h *ChatHandler) handleSequentialChain(w http.ResponseWriter, r *http.Request, c chain.Chain, req *schema.RequestEnvelope) {
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

		if i == len(c.Models)-1 && req.Stream {
			h.streamChainStep(w, r, c.Name, model, stepMessages)
			return
		}

		result := h.runChainStep(r, c.Name, model, stepMessages)
		if result.err != nil {
			slog.Error("chain step failed", "chain", c.Name, "step", i, "model", model, "err", result.err)
			writeChainStepError(w, model, result.err)
			return
		}
		resp = result.resp
	}

	resp.Model = c.Name
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}

// handleFusionChain runs c.Models in parallel against the caller's original
// messages, then routes their combined answers to c.Judge for synthesis —
// ixr's equivalent of OmniRoute's parallel-panel-plus-judge routing
// strategy. Unlike a sequential chain, a single panel member failing does
// not abort the request: the judge synthesizes from whichever panel
// members succeeded, and only an all-panel failure is terminal. The judge
// call honours req.Stream, since it's always the terminal call.
func (h *ChatHandler) handleFusionChain(w http.ResponseWriter, r *http.Request, c chain.Chain, req *schema.RequestEnvelope) {
	base := append([]schema.Message(nil), req.Messages...)

	results := make([]stepResult, len(c.Models))
	var wg sync.WaitGroup
	for i, model := range c.Models {
		wg.Add(1)
		go func(i int, model string) {
			defer wg.Done()
			stepMessages := append([]schema.Message(nil), base...)
			results[i] = h.runChainStep(r, c.Name, model, stepMessages)
		}(i, model)
	}
	wg.Wait()

	anySucceeded := false
	for _, res := range results {
		if res.err == nil {
			anySucceeded = true
			break
		}
	}
	if !anySucceeded {
		slog.Error("fusion panel all failed", "chain", c.Name)
		writeError(w, http.StatusBadGateway, "fusion_panel_failed", "all fusion panel models failed for chain "+c.Name)
		return
	}

	judgeMessages := buildJudgeMessages(base, results)

	if req.Stream {
		h.streamChainStep(w, r, c.Name, c.Judge, judgeMessages)
		return
	}

	judgeResult := h.runChainStep(r, c.Name, c.Judge, judgeMessages)
	if judgeResult.err != nil {
		slog.Error("fusion judge failed", "chain", c.Name, "judge", c.Judge, "err", judgeResult.err)
		writeChainStepError(w, c.Judge, judgeResult.err)
		return
	}

	resp := judgeResult.resp
	resp.Model = c.Name
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}

// buildJudgeMessages wraps the caller's original messages with the panel's
// candidate answers (failed panel members are omitted) as a synthesis
// instruction for the judge model.
func buildJudgeMessages(original []schema.Message, results []stepResult) []schema.Message {
	msgs := append([]schema.Message(nil), original...)

	var b strings.Builder
	b.WriteString("You were given independent candidate answers from different models to the preceding request. Synthesize a single best final answer, combining their strengths and resolving any disagreements between them.\n\n")
	for _, res := range results {
		if res.err != nil {
			continue
		}
		b.WriteString("Candidate (" + res.model + "):\n" + res.resp.Choices[0].Message.Content + "\n\n")
	}

	return append(msgs, schema.Message{Role: "user", Content: b.String()})
}

// stepResult carries a chain step's outcome for either strategy.
type stepResult struct {
	model string
	resp  *schema.ResponseEnvelope
	err   error
}

// runChainStep executes one model call within a chain (sequential step or
// fusion panel member): circuit breaker check, retry-safe execution,
// metrics, and a per-step CallEvent.
func (h *ChatHandler) runChainStep(r *http.Request, chainName, model string, stepMessages []schema.Message) stepResult {
	id := identity.FromContext(r.Context())
	stepReq := &schema.RequestEnvelope{Model: model, Messages: stepMessages}

	if h.cbRegistry != nil && !h.cbRegistry.IsAllowed(model) {
		slog.Error("chain step blocked", "chain", chainName, "model", model, "reason", "circuit breaker open")
		return stepResult{model: model, err: errCircuitOpen}
	}

	start := time.Now()
	result, err := routing.Execute(r.Context(), routing.RoutingDecision{Model: model}, reasoning.AdjustTokenBudget(stepReq), routing.ProviderLookup(h.router), h.retryCfg)
	latency := time.Since(start)

	if h.cbRegistry != nil && shouldRecordOutcome(err) {
		h.cbRegistry.RecordOutcome(model, err == nil)
	}

	if h.metrics != nil {
		providerName := ""
		if result.Provider != nil {
			providerName = result.Provider.Name()
		}
		status := http.StatusOK
		var tokIn, tokOut int
		if err != nil {
			status = http.StatusBadGateway
		} else {
			tokIn = result.Response.Usage.PromptTokens
			tokOut = result.Response.Usage.CompletionTokens
		}
		h.metrics.Record(providerName, model, status, latency, tokIn, tokOut)
	}

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
			ev.Cost = cost.ForUsage(model, result.Response.Usage)
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error (chain step)", "err", pubErr)
		}
	}

	if err != nil {
		return stepResult{model: model, err: err}
	}
	return stepResult{model: model, resp: result.Response}
}

// streamChainStep executes a chain's terminal model call (last sequential
// step, or the fusion judge) as SSE, mirroring handleStream's per-chunk
// write path. It is the only chain step that can stream: earlier steps
// need each other's full text to build the next prompt.
func (h *ChatHandler) streamChainStep(w http.ResponseWriter, r *http.Request, chainName, model string, stepMessages []schema.Message) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_error", "server does not support streaming")
		return
	}

	if h.cbRegistry != nil && !h.cbRegistry.IsAllowed(model) {
		writeError(w, http.StatusServiceUnavailable, "circuit_open", "chain step for model "+model+" unavailable (circuit breaker open)")
		return
	}

	writeSSEHeader(w)

	var totalIn, totalOut, totalCacheRead, totalCacheCreation int
	start := time.Now()
	stepReq := &schema.RequestEnvelope{Model: model, Messages: stepMessages}

	decision := routing.RoutingDecision{Model: model}
	result, streamErr := routing.ExecuteStream(r.Context(), decision, reasoning.AdjustTokenBudget(stepReq), routing.ProviderLookup(h.router), h.retryCfg, func(chunk provider.StreamChunk) error {
		if chunk.Usage != nil {
			totalIn = chunk.Usage.PromptTokens
			totalOut = chunk.Usage.CompletionTokens
			totalCacheRead = chunk.Usage.CacheReadInputTokens
			totalCacheCreation = chunk.Usage.CacheCreationInputTokens
		}
		chunk.Model = chainName
		if err := writeSSEChunk(w, chunk); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})

	if h.cbRegistry != nil && shouldRecordOutcome(streamErr) {
		h.cbRegistry.RecordOutcome(model, streamErr == nil)
	}

	writeSSEDone(w)
	flusher.Flush()

	latency := time.Since(start)
	if h.metrics != nil {
		providerName := ""
		if result.Provider != nil {
			providerName = result.Provider.Name()
		}
		status := http.StatusOK
		if streamErr != nil {
			status = http.StatusBadGateway
		}
		h.metrics.Record(providerName, model, status, latency, totalIn, totalOut)
	}

	if h.bus != nil {
		id := identity.FromContext(r.Context())
		ev := &schema.CallEvent{
			Timestamp: start,
			Model:     model,
			Latency:   schema.EventLatency(latency),
			Request:   *stepReq,
			UseCaseID: r.Header.Get("X-IXR-UseCase"),
			TenantID:  id.TenantID,
			TeamID:    id.TeamID,
			UserID:    id.UserID,
			TokensIn:  totalIn,
			TokensOut: totalOut,
			Streaming: true,
		}
		if result.Provider != nil {
			ev.Provider = result.Provider.Name()
		}
		if streamErr != nil {
			ev.Error = streamErr.Error()
		} else {
			ev.Cost = cost.ForUsage(model, schema.Usage{
				PromptTokens:             totalIn,
				CompletionTokens:         totalOut,
				CacheReadInputTokens:     totalCacheRead,
				CacheCreationInputTokens: totalCacheCreation,
			})
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error (chain stream step)", "err", pubErr)
		}
	}

	if streamErr != nil {
		slog.Error("chain stream step error", "chain", chainName, "model", model, "err", streamErr)
	}
}

// writeChainStepError writes the appropriate HTTP error for a failed chain
// step, distinguishing a circuit-breaker rejection from an upstream error.
func writeChainStepError(w http.ResponseWriter, model string, err error) {
	if errors.Is(err, errCircuitOpen) {
		writeError(w, http.StatusServiceUnavailable, "circuit_open", "chain step for model "+model+" unavailable (circuit breaker open)")
		return
	}
	writeError(w, http.StatusBadGateway, "chain_step_failed", "chain step for model "+model+" failed: upstream provider returned an error")
}
