package ingress

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	if req.Model == "auto" {
		hint := taskHintFromHeaders(r, &req)
		if h.engine != nil {
			decision, err := h.engine.Decide(r.Context(), hint, h.cbRegistry)
			if err != nil || decision.Model == "" {
				writeError(w, http.StatusBadRequest, "auto_route_failed", "no catalog model matched the given constraints")
				return
			}
			req.Model = decision.Model
		} else {
			resolved := routing.Route(hint)
			if resolved == "" {
				writeError(w, http.StatusBadRequest, "auto_route_failed", "no catalog model matched the given budget and task constraints")
				return
			}
			req.Model = resolved
		}
	}

	p, err := h.router(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_provider", err.Error())
		return
	}

	if req.Stream {
		h.handleStream(w, r, p, &req)
		return
	}

	start := time.Now()
	resp, err := p.Chat(r.Context(), reasoning.AdjustTokenBudget(&req))
	latency := time.Since(start)

	if h.metrics != nil {
		status := http.StatusOK
		var tokIn, tokOut int
		if err != nil {
			status = http.StatusBadGateway
		} else {
			tokIn = resp.Usage.PromptTokens
			tokOut = resp.Usage.CompletionTokens
		}
		h.metrics.Record(p.Name(), req.Model, status, latency, tokIn, tokOut)
	}

	if h.bus != nil {
		id := identity.FromContext(r.Context())
		ev := &schema.CallEvent{
			Timestamp: start,
			Provider:  p.Name(),
			Model:     req.Model,
			Latency:   schema.EventLatency(latency),
			Request:   req,
			UseCaseID: r.Header.Get("X-IXR-UseCase"),
			TenantID:  id.TenantID,
			TeamID:    id.TeamID,
			UserID:    id.UserID,
		}
		if err != nil {
			ev.Error = err.Error()
		} else {
			ev.ID = resp.ID
			ev.TokensIn = resp.Usage.PromptTokens
			ev.TokensOut = resp.Usage.CompletionTokens
			ev.Response = *resp
			ev.Cost = cost.ForUsage(req.Model, ev.TokensIn, ev.TokensOut)
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
		slog.Error("provider error", "provider", p.Name(), "model", req.Model, "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider returned an error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}

func (h *ChatHandler) handleStream(w http.ResponseWriter, r *http.Request, p provider.Provider, req *schema.RequestEnvelope) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_error", "server does not support streaming")
		return
	}

	writeSSEHeader(w)

	var totalIn, totalOut int
	start := time.Now()

	streamErr := p.Stream(r.Context(), reasoning.AdjustTokenBudget(req), func(chunk provider.StreamChunk) error {
		if chunk.Usage != nil {
			totalIn = chunk.Usage.PromptTokens
			totalOut = chunk.Usage.CompletionTokens
		}
		if err := writeSSEChunk(w, chunk); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})

	writeSSEDone(w)
	flusher.Flush()

	streamLatency := time.Since(start)
	if h.metrics != nil {
		status := http.StatusOK
		if streamErr != nil {
			status = http.StatusBadGateway
		}
		h.metrics.Record(p.Name(), req.Model, status, streamLatency, totalIn, totalOut)
	}

	if h.bus != nil {
		id := identity.FromContext(r.Context())
		ev := &schema.CallEvent{
			Timestamp: start,
			Provider:  p.Name(),
			Model:     req.Model,
			Latency:   schema.EventLatency(streamLatency),
			Request:   *req,
			UseCaseID: r.Header.Get("X-IXR-UseCase"),
			TenantID:  id.TenantID,
			TeamID:    id.TeamID,
			UserID:    id.UserID,
			TokensIn:  totalIn,
			TokensOut: totalOut,
			Streaming: true,
		}
		if streamErr != nil {
			ev.Error = streamErr.Error()
		} else {
			ev.Cost = cost.ForUsage(req.Model, totalIn, totalOut)
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error (stream)", "err", pubErr)
		}
	}

	if streamErr != nil {
		slog.Error("stream error", "provider", p.Name(), "model", req.Model, "err", streamErr)
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

	resp, err := sp.Chat(ctx, &shadowReq)
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

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{
		Error: apiErrorBody{Message: message, Type: errType},
	})
}
