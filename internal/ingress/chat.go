package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/internal/domain/cost"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/reasoning"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/internal/observability"
	"github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// headerShadowModel is the request header that triggers inline shadow routing.
const headerShadowModel = "X-IXR-Shadow-Model"

// Router picks a provider for a given model name.
type Router func(model string) (provider.Provider, error)

// ChatOption applies optional configuration to a ChatHandler.
type ChatOption func(*ChatHandler)

// WithEngine configures a live scoring engine for model="auto" routing.
func WithEngine(e *scoring.Engine) ChatOption {
	return func(h *ChatHandler) { h.engine = e }
}

// WithCBRegistry wires a circuit breaker registry into auto-routing decisions.
func WithCBRegistry(cb *circuitbreaker.Registry) ChatOption {
	return func(h *ChatHandler) { h.cbRegistry = cb }
}

// WithShadow attaches a shadow routing orchestrator.
func WithShadow(s *scoring.Orchestrator) ChatOption {
	return func(h *ChatHandler) { h.shadow = s }
}

// WithRetryConfig overrides the default retry/backoff settings.
func WithRetryConfig(cfg routing.RetryConfig) ChatOption {
	return func(h *ChatHandler) { h.retryCfg = cfg }
}

// WithMetrics wires Prometheus request/latency/token metrics into the handler.
func WithMetrics(m *observability.Metrics) ChatOption {
	return func(h *ChatHandler) { h.metrics = m }
}

// WithChains registers named model chains (see docs/rfc/0001-semantic-cache.md
// Gap 11), dispatched when "model" names a chain instead of a real model.
func WithChains(reg chain.Registry) ChatOption {
	return func(h *ChatHandler) { h.chains = reg }
}

// ChatHandler handles POST /v1/chat/completions.
// It is OpenAI-compatible: existing SDKs point at ixr with no code changes.
type ChatHandler struct {
	router     Router
	bus        bus.Bus
	engine     *scoring.Engine
	cbRegistry *circuitbreaker.Registry
	shadow     *scoring.Orchestrator
	retryCfg   routing.RetryConfig
	metrics    *observability.Metrics
	chains     chain.Registry
}

// NewChatHandler creates a handler that delegates to router for provider selection.
// Pass a non-nil bus to emit CallEvents after each request.
func NewChatHandler(router Router, b bus.Bus, opts ...ChatOption) *ChatHandler {
	h := &ChatHandler{router: router, bus: b, retryCfg: routing.DefaultRetryConfig}
	for _, o := range opts {
		o(h)
	}
	return h
}

func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	var req schema.RequestEnvelope
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
		return
	}

	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "missing_model", "model field is required")
		return
	}

	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "missing_messages", "messages field must contain at least one message")
		return
	}

	if c, ok := h.chains.Lookup(req.Model); ok {
		h.handleChain(w, r, c, &req)
		return
	}

	hint := taskHintFromHeaders(r, &req)
	var fallbackChain []routing.Candidate

	autoRouted := req.Model == "auto"
	if autoRouted {
		engineUsed := "static"
		if h.engine != nil {
			engineUsed = "adaptive"
			decision, err := h.engine.Decide(r.Context(), hint, h.cbRegistry)
			if err != nil || decision.Model == "" {
				slog.Warn("auto-routing found no eligible candidate", "engine", engineUsed, "err", err)
				writeError(w, http.StatusBadRequest, "auto_route_failed", "no catalog model matched the given constraints")
				return
			}
			req.Model = decision.Model
			fallbackChain = decision.FallbackChain
		} else {
			decision := routing.RouteWithDecision(hint)
			if decision.Model == "" {
				slog.Warn("auto-routing found no eligible candidate", "engine", engineUsed, "prompt_chars", hint.PromptChars)
				writeError(w, http.StatusBadRequest, "auto_route_failed", "no catalog model matched the given budget and task constraints")
				return
			}
			req.Model = decision.Model
			fallbackChain = decision.FallbackChain
		}
		// Debug, not Info: this fires on every auto-routed request, and the
		// decision that matters operationally (what got used, what failed)
		// is already covered by the Warn/Error paths below and by the
		// CallEvent/TelemetryRecord published once a response comes back.
		// This is for the "why did it pick that" question specifically.
		slog.Debug("auto-routing decision",
			"engine", engineUsed,
			"primary", req.Model,
			"fallback_chain", candidateModels(fallbackChain),
			"reasoning_score", hint.ReasoningScore,
			"coding_score", hint.CodingScore,
			"math_score", hint.MathScore,
			"multilingual_score", hint.MultilingualScore,
			"max_cost_usd_per_1m", hint.MaxCostUSDPer1M,
			"latency_sensitive", hint.LatencySensitive,
		)
	} else if _, inCatalog := routing.Lookup(req.Model); inCatalog {
		// Escalation only applies when we have real ContextWindow data to
		// escalate against. For an explicit model outside the catalog (the
		// common case — most deployed models aren't catalog entries), there's
		// no chain to escalate through, so behavior is unchanged: a single
		// direct call, surfaced as a 502 on failure.
		fallbackChain = routing.FallbackChainFor(req.Model, hint, 3)
	}

	p, err := h.router(req.Model)
	if err != nil {
		if autoRouted {
			// The case that started this: auto-routing can pick a model with
			// no configured provider, and the fallback chain built above is
			// never consulted here — chat.go returns before Execute() (which
			// is the only thing that walks it) is ever called. Logging the
			// chain anyway shows whether a viable candidate existed and was
			// simply never tried.
			slog.Warn("auto-routing picked a model with no configured provider",
				"model", req.Model,
				"fallback_chain", candidateModels(fallbackChain),
				"err", err,
			)
		}
		writeError(w, http.StatusBadRequest, "no_provider", err.Error())
		return
	}

	if req.Stream {
		h.handleStream(w, r, p, &req, autoRouted, fallbackChain)
		return
	}

	if h.cbRegistry != nil && !h.cbRegistry.IsAllowed(req.Model) {
		writeError(w, http.StatusServiceUnavailable, "circuit_open", "model temporarily unavailable (circuit breaker open)")
		return
	}

	start := time.Now()
	var resp *schema.ResponseEnvelope
	var result routing.ExecuteResult
	if len(fallbackChain) > 0 {
		decision := routing.RoutingDecision{Model: req.Model, FallbackChain: fallbackChain}
		result, err = routing.Execute(r.Context(), decision, reasoning.AdjustTokenBudget(&req), routing.ProviderLookup(h.router), h.retryCfg)
		resp = result.Response
		// result reflects whichever candidate actually produced the outcome —
		// on failure that's the last one attempted, not necessarily the
		// primary — so p/req.Model must not be used below this point.
		if result.Provider != nil {
			p = result.Provider
		}
	} else {
		resp, err = p.Chat(r.Context(), reasoning.AdjustTokenBudget(&req))
		result.Model = req.Model
	}
	latency := time.Since(start)
	usedModel := req.Model
	if result.Model != "" {
		usedModel = result.Model
	}
	usedProvider := ""
	if p != nil {
		usedProvider = p.Name()
	}

	if h.cbRegistry != nil && shouldRecordOutcome(err) {
		h.cbRegistry.RecordOutcome(req.Model, err == nil)
	}

	if h.metrics != nil {
		status := http.StatusOK
		var tokIn, tokOut int
		if err != nil {
			status = http.StatusBadGateway
		} else {
			tokIn = resp.Usage.PromptTokens
			tokOut = resp.Usage.CompletionTokens
		}
		h.metrics.Record(usedProvider, usedModel, status, latency, tokIn, tokOut)
	}

	if h.bus != nil {
		id := identity.FromContext(r.Context())
		ev := &schema.CallEvent{
			Timestamp:  start,
			Provider:   usedProvider,
			Model:      usedModel,
			Latency:    schema.EventLatency(latency),
			Request:    req,
			UseCaseID:  r.Header.Get("X-IXR-UseCase"),
			TenantID:   id.TenantID,
			TeamID:     id.TeamID,
			UserID:     id.UserID,
			AutoRouted: autoRouted,
		}
		if err != nil {
			ev.Error = err.Error()
		} else {
			ev.ID = resp.ID
			ev.TokensIn = resp.Usage.PromptTokens
			ev.TokensOut = resp.Usage.CompletionTokens
			ev.Response = *resp
			ev.Cost = cost.ForUsage(usedModel, ev.TokensIn, ev.TokensOut)
			ev.FallbackUsed = result.FallbackUsed
			ev.FallbackFrom = result.FallbackFrom
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error", "err", pubErr)
		}

		if shadowModel := r.Header.Get(headerShadowModel); shadowModel != "" && shadowModel != req.Model {
			primaryID := ""
			if resp != nil {
				primaryID = resp.ID
			}
			go h.runShadow(r, primaryID, req.Model, shadowModel, &req)
		}
	}

	if err != nil {
		slog.Error("provider error", "provider", usedProvider, "model", usedModel, "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider returned an error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}

func (h *ChatHandler) handleStream(w http.ResponseWriter, r *http.Request, p provider.Provider, req *schema.RequestEnvelope, autoRouted bool, fallbackChain []routing.Candidate) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_error", "server does not support streaming")
		return
	}

	if h.cbRegistry != nil && !h.cbRegistry.IsAllowed(req.Model) {
		writeError(w, http.StatusServiceUnavailable, "circuit_open", "model temporarily unavailable (circuit breaker open)")
		return
	}

	writeSSEHeader(w)

	var totalIn, totalOut int
	start := time.Now()

	onChunk := func(chunk provider.StreamChunk) error {
		if chunk.Usage != nil {
			totalIn = chunk.Usage.PromptTokens
			totalOut = chunk.Usage.CompletionTokens
		}
		if err := writeSSEChunk(w, chunk); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	var streamErr error
	var result routing.ExecuteResult
	if len(fallbackChain) > 0 {
		decision := routing.RoutingDecision{Model: req.Model, FallbackChain: fallbackChain}
		result, streamErr = routing.ExecuteStream(r.Context(), decision, reasoning.AdjustTokenBudget(req), routing.ProviderLookup(h.router), h.retryCfg, onChunk)
		// result reflects whichever candidate actually produced the outcome —
		// on failure that's the last one attempted, not necessarily the
		// primary — so p/req.Model must not be used below this point.
		if result.Provider != nil {
			p = result.Provider
		}
	} else {
		streamErr = p.Stream(r.Context(), reasoning.AdjustTokenBudget(req), onChunk)
		result.Model = req.Model
	}

	if h.cbRegistry != nil && shouldRecordOutcome(streamErr) {
		h.cbRegistry.RecordOutcome(req.Model, streamErr == nil)
	}

	writeSSEDone(w)
	flusher.Flush()

	streamLatency := time.Since(start)
	usedModel := req.Model
	if result.Model != "" {
		usedModel = result.Model
	}
	usedProvider := ""
	if p != nil {
		usedProvider = p.Name()
	}

	if h.metrics != nil {
		status := http.StatusOK
		if streamErr != nil {
			status = http.StatusBadGateway
		}
		h.metrics.Record(usedProvider, usedModel, status, streamLatency, totalIn, totalOut)
	}

	if h.bus != nil {
		id := identity.FromContext(r.Context())
		ev := &schema.CallEvent{
			Timestamp:  start,
			Provider:   usedProvider,
			Model:      usedModel,
			Latency:    schema.EventLatency(streamLatency),
			Request:    *req,
			UseCaseID:  r.Header.Get("X-IXR-UseCase"),
			TenantID:   id.TenantID,
			TeamID:     id.TeamID,
			UserID:     id.UserID,
			TokensIn:   totalIn,
			TokensOut:  totalOut,
			AutoRouted: autoRouted,
			Streaming:  true,
		}
		if streamErr != nil {
			ev.Error = streamErr.Error()
		} else {
			ev.Cost = cost.ForUsage(usedModel, totalIn, totalOut)
			ev.FallbackUsed = result.FallbackUsed
			ev.FallbackFrom = result.FallbackFrom
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error (stream)", "err", pubErr)
		}
	}

	if streamErr != nil {
		slog.Error("stream error", "provider", usedProvider, "model", usedModel, "err", streamErr)
	}
}

func (h *ChatHandler) runShadow(r *http.Request, primaryID, primaryModel, shadowModel string, req *schema.RequestEnvelope) {
	ctx := context.Background()
	shadowReq := *req
	shadowReq.Model = shadowModel

	meta := &schema.ShadowMetadata{
		PrimaryID:    primaryID,
		PrimaryModel: primaryModel,
		ShadowModel:  shadowModel,
	}
	id := identity.FromContext(r.Context())
	start := time.Now()
	ev := &schema.CallEvent{
		Timestamp: start,
		Model:     shadowModel,
		UseCaseID: r.Header.Get("X-IXR-UseCase"),
		TenantID:  id.TenantID,
		TeamID:    id.TeamID,
		UserID:    id.UserID,
		Request:   shadowReq,
		Shadow:    meta,
	}

	sp, err := h.router(shadowModel)
	if err != nil {
		ev.Error = err.Error()
		ev.Latency = schema.EventLatency(time.Since(start))
		_ = h.bus.Publish(ctx, ev)
		return
	}
	ev.Provider = sp.Name()

	resp, err := sp.Chat(ctx, reasoning.AdjustTokenBudget(&shadowReq))
	ev.Latency = schema.EventLatency(time.Since(start))
	if err != nil {
		ev.Error = err.Error()
	} else {
		ev.ID = resp.ID
		ev.TokensIn = resp.Usage.PromptTokens
		ev.TokensOut = resp.Usage.CompletionTokens
		ev.Response = *resp
		ev.Cost = cost.ForUsage(shadowModel, ev.TokensIn, ev.TokensOut)
	}
	_ = h.bus.Publish(ctx, ev)
}

// shouldRecordOutcome reports whether err should influence a model's circuit
// breaker state. A context cancellation or deadline expiry reflects the
// caller giving up (or the caller's own deadline), not the provider
// failing — every provider call in this codebase shares the caller's
// request context with no internal timeout layered on top (confirmed: no
// context.WithTimeout/WithDeadline/WithCancel wraps a provider call
// anywhere in this package or internal/domain/routing), so these two error
// types never carry information about the model's actual health here.
// Counting them as failures anyway trips breakers on perfectly healthy
// models the moment the system is under enough load that callers start
// timing out across the board — removing capacity at exactly the point a
// self-amplifying cascade is most likely, in response to load, not to any
// real problem with that model. Found via a load-test profiling pass: at
// high enough concurrency, models never configured to fail started
// tripping their breakers via context-canceled errors alone.
func shouldRecordOutcome(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func promptCharsFromMessages(req *schema.RequestEnvelope) int {
	if req == nil {
		return 0
	}
	n := 0
	for _, m := range req.Messages {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.ID) + len(tc.Type) + len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

func taskHintFromHeaders(r *http.Request, req *schema.RequestEnvelope) routing.TaskHint {
	hint := routing.TaskHint{
		PromptChars: promptCharsFromMessages(req),
	}
	if r == nil {
		return hint
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-IXR-Task"))) {
	case "reasoning":
		hint.ReasoningScore = 1
	case "coding":
		hint.CodingScore = 1
	case "math":
		hint.MathScore = 1
	case "multilingual":
		hint.MultilingualScore = 1
	}
	if v := strings.TrimSpace(r.Header.Get("X-IXR-Budget")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			hint.MaxCostUSDPer1M = f
		}
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-IXR-Latency")), "sensitive") {
		hint.LatencySensitive = true
	}
	return hint
}

// apiError matches the OpenAI error envelope so existing SDKs parse it correctly.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// candidateModels extracts just the model IDs from a candidate list, for
// compact logging — a log line doesn't need the score field's precision.
func candidateModels(candidates []routing.Candidate) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.Model
	}
	return out
}

// writeError previously wrote the error response only — every early-return
// failure (a request that never got far enough to publish a CallEvent, e.g.
// model:"auto" landing on a model with no configured provider) left zero
// trace server-side. A caller saw a 4xx/5xx; the server logged nothing.
func writeError(w http.ResponseWriter, status int, errType, message string) {
	if status >= 500 {
		slog.Error("request failed", "status", status, "error_type", errType, "message", message)
	} else {
		slog.Warn("request failed", "status", status, "error_type", errType, "message", message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{
		Error: apiErrorBody{Message: message, Type: errType},
	})
}
