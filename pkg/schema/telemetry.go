package schema

import "time"

// TelemetryRecord is the extended record written by the telemetry plugin.
// It carries routing-decision metadata beyond what CallEvent holds,
// and is what the scoring engine reads to update the model performance store.
type TelemetryRecord struct {
	RequestID    string    `json:"request_id"`
	UseCaseID    string    `json:"use_case_id"`
	TenantID     string    `json:"tenant_id"`
	Intent       string    `json:"intent"`
	Model         string    `json:"model"`
	ResponseModel string    `json:"response_model,omitempty"` // actual model from provider (may differ)
	Provider      string    `json:"provider"`
	LatencyMS     int       `json:"latency_ms"`
	TokensIn      int       `json:"tokens_in"`
	TokensOut     int       `json:"tokens_out"`
	MaxTokens     int       `json:"max_tokens,omitempty"`
	CostUSD       float64   `json:"cost_usd"`
	Success       bool      `json:"success"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	FinishReason  string    `json:"finish_reason"`
	FallbackUsed bool      `json:"fallback_used"` // was the primary model bypassed?
	FallbackFrom string    `json:"fallback_from"` // which model failed
	Shadow       bool      `json:"shadow"`
	ShadowOf     string    `json:"shadow_of,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}
