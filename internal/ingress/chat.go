package ingress

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

const (
	headerShadowModel     = "X-IXR-Shadow-Model"
	headerShadowTimeoutMS = "X-IXR-Shadow-Timeout-MS"
	defaultShadowTimeout  = 30 * time.Second
)

// Router picks a provider for a given model name.
type Router func(model string) (provider.Provider, error)

// ChatHandler handles POST /v1/chat/completions.
// It is OpenAI-compatible: existing SDKs point at ixr with no code changes.
type ChatHandler struct {
	router Router
	bus    bus.Bus
}

// NewChatHandler creates a handler that delegates to router for provider selection.
// Pass a non-nil bus to emit CallEvents after each request.
func NewChatHandler(router Router, b bus.Bus) *ChatHandler {
	return &ChatHandler{router: router, bus: b}
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

	// Streaming is phase 2 — reject early with a clear message.
	if req.Stream {
		writeError(w, http.StatusNotImplemented, "streaming_not_supported", "streaming is not yet supported; set stream=false")
		return
	}

	if req.Model == "auto" {
		hint := taskHintFromHeaders(r, &req)
		resolved := routing.Route(hint)
		if resolved == "" {
			writeError(w, http.StatusBadRequest, "auto_route_failed", "no catalog model matched the given budget and task constraints")
			return
		}
		req.Model = resolved
	}

	p, err := h.router(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_provider", err.Error())
		return
	}

	start := time.Now()
	resp, err := p.Chat(r.Context(), &req)
	latency := time.Since(start)

	if h.bus != nil {
		ev := &schema.CallEvent{
			Timestamp: start,
			Provider:  p.Name(),
			Model:     req.Model,
			Latency:   latency,
			Request:   req,
			UseCaseID: r.Header.Get("X-IXR-UseCase"),
		}
		if err != nil {
			ev.Error = err.Error()
		} else {
			ev.ID = resp.ID
			ev.TokensIn = resp.Usage.PromptTokens
			ev.TokensOut = resp.Usage.CompletionTokens
			ev.Response = *resp
		}
		if pubErr := h.bus.Publish(r.Context(), ev); pubErr != nil {
			slog.Warn("bus publish error", "err", pubErr)
		}
	}

	if err != nil {
		slog.Error("provider error", "provider", p.Name(), "model", req.Model, "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider returned an error")
		return
	}

	h.maybeRunShadow(r, req, resp)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write response", "err", err)
	}
}

func (h *ChatHandler) maybeRunShadow(r *http.Request, primaryReq schema.RequestEnvelope, primaryResp *schema.ResponseEnvelope) {
	if h.bus == nil || primaryResp == nil {
		return
	}

	shadowModel := strings.TrimSpace(r.Header.Get(headerShadowModel))
	decision := scoring.PlanShadow(
		scoring.Candidate{Model: primaryReq.Model},
		scoring.ShadowPolicy{Enabled: shadowModel != "", Model: scoring.Candidate{Model: shadowModel}},
	)
	if !decision.Enabled {
		return
	}

	shadowReq := cloneRequest(primaryReq)
	shadowReq.Model = decision.Model.Model
	useCaseID := r.Header.Get("X-IXR-UseCase")
	primaryID := primaryResp.ID
	primaryModel := primaryReq.Model
	timeout := shadowTimeout(r)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		p, err := h.router(shadowReq.Model)
		if err != nil {
			h.publishShadowEvent(ctx, useCaseID, primaryID, primaryModel, shadowReq, "", 0, nil, err)
			return
		}

		start := time.Now()
		resp, err := p.Chat(ctx, &shadowReq)
		latency := time.Since(start)
		h.publishShadowEvent(ctx, useCaseID, primaryID, primaryModel, shadowReq, p.Name(), latency, resp, err)
	}()
}

func (h *ChatHandler) publishShadowEvent(
	ctx context.Context,
	useCaseID string,
	primaryID string,
	primaryModel string,
	shadowReq schema.RequestEnvelope,
	providerName string,
	latency time.Duration,
	resp *schema.ResponseEnvelope,
	err error,
) {
	ev := &schema.CallEvent{
		Timestamp: time.Now(),
		UseCaseID: useCaseID,
		Provider:  providerName,
		Model:     shadowReq.Model,
		Latency:   latency,
		Request:   shadowReq,
		Shadow: &schema.ShadowMetadata{
			PrimaryID:    primaryID,
			PrimaryModel: primaryModel,
			ShadowModel:  shadowReq.Model,
		},
	}
	if err != nil {
		ev.Error = err.Error()
	} else if resp != nil {
		ev.ID = resp.ID
		ev.TokensIn = resp.Usage.PromptTokens
		ev.TokensOut = resp.Usage.CompletionTokens
		ev.Response = *resp
	}

	if pubErr := h.bus.Publish(ctx, ev); pubErr != nil {
		slog.Warn("shadow bus publish error", "err", pubErr)
	}
}

func shadowTimeout(r *http.Request) time.Duration {
	raw := strings.TrimSpace(r.Header.Get(headerShadowTimeoutMS))
	if raw == "" {
		return defaultShadowTimeout
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return defaultShadowTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func cloneRequest(req schema.RequestEnvelope) schema.RequestEnvelope {
	out := req
	if req.Messages == nil {
		return out
	}
	out.Messages = make([]schema.Message, len(req.Messages))
	copy(out.Messages, req.Messages)
	for i := range req.Messages {
		if req.Messages[i].ToolCalls == nil {
			continue
		}
		out.Messages[i].ToolCalls = make([]schema.ToolCall, len(req.Messages[i].ToolCalls))
		copy(out.Messages[i].ToolCalls, req.Messages[i].ToolCalls)
	}
	return out
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
