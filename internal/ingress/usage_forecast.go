package ingress

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/usageforecast"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// UsageForecastHandler handles GET /v1/usage/forecast.
type UsageForecastHandler struct {
	service *usageforecast.Service
}

// NewUsageForecastHandler creates a usage forecasting endpoint.
func NewUsageForecastHandler(service *usageforecast.Service) *UsageForecastHandler {
	return &UsageForecastHandler{service: service}
}

func (h *UsageForecastHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	q := r.URL.Query()
	userID := strings.TrimSpace(q.Get("user_id"))
	if userID == "" {
		userID = strings.TrimSpace(r.Header.Get("X-IXR-User"))
	}

	req := schema.TokenForecastRequest{
		UserID:         userID,
		Model:          strings.TrimSpace(q.Get("model")),
		Window:         durationFromHours(q.Get("window_hours"), usageforecast.DefaultWindow),
		Horizon:        durationFromHours(q.Get("horizon_hours"), usageforecast.DefaultHorizon),
		Bucket:         durationFromMinutes(q.Get("bucket_minutes"), usageforecast.DefaultBucket),
		FreeTokenLimit: intFromQuery(q.Get("free_token_limit")),
	}
	resp, err := h.service.Forecast(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "usage_forecast_failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		writeError(w, http.StatusInternalServerError, "response_write_failed", "failed to write response")
	}
}

// UsageForecastJobHandler handles asynchronous forecast jobs for the spike path.
type UsageForecastJobHandler struct {
	orchestrator *usageforecast.JobOrchestrator
}

// NewUsageForecastJobHandler creates an async usage forecasting endpoint.
func NewUsageForecastJobHandler(orchestrator *usageforecast.JobOrchestrator) *UsageForecastJobHandler {
	return &UsageForecastJobHandler{orchestrator: orchestrator}
}

func (h *UsageForecastJobHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r)
	case http.MethodGet:
		h.get(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are supported")
	}
}

func (h *UsageForecastJobHandler) create(w http.ResponseWriter, r *http.Request) {
	var params schema.TokenForecastJobParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "could not parse request JSON")
		return
	}
	if params.UserID == "" {
		params.UserID = strings.TrimSpace(r.Header.Get("X-IXR-User"))
	}
	job, err := h.orchestrator.Enqueue(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "forecast_job_enqueue_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(job)
}

func (h *UsageForecastJobHandler) get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/usage/forecast/jobs/")
	id = strings.TrimSpace(id)
	if id == "" || id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "missing_job_id", "forecast job id is required")
		return
	}
	job, err := h.orchestrator.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "forecast_job_not_found", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func durationFromHours(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return time.Duration(v * float64(time.Hour))
}

func durationFromMinutes(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return time.Duration(v * float64(time.Minute))
}

func intFromQuery(raw string) int {
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
