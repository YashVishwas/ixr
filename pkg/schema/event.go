// Package schema contains the public data contracts for ixr.
// Everything in this package is semver-governed; third parties build on these types.
package schema

import (
	"encoding/json"
	"time"
)

// EventLatency is a call duration serialized as a plain integer number of
// milliseconds. time.Duration has no custom MarshalJSON — it serializes as
// raw nanoseconds — which silently mismatched the "latency_ms" tag below for
// any consumer reading CallEvent JSON directly (audit log, webhook fanout).
// Behaves like time.Duration for callers (Milliseconds() is preserved).
type EventLatency time.Duration

func (d EventLatency) Milliseconds() int64 { return time.Duration(d).Milliseconds() }

func (d EventLatency) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Milliseconds())
}

func (d *EventLatency) UnmarshalJSON(b []byte) error {
	var ms int64
	if err := json.Unmarshal(b, &ms); err != nil {
		return err
	}
	*d = EventLatency(ms * int64(time.Millisecond))
	return nil
}

// CallEvent is emitted on the bus for every LLM call that passes through ixr.
// It is the primary unit of data the intelligence layer consumes.
type CallEvent struct {
	ID        string           `json:"id"`
	Timestamp time.Time        `json:"timestamp"`
	UseCaseID string           `json:"use_case_id"`       // from X-IXR-UseCase header
	TenantID  string           `json:"tenant_id"`         // from identity context
	TeamID    string           `json:"team_id,omitempty"` // from identity context
	UserID    string           `json:"user_id,omitempty"` // from identity context
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
	Latency   EventLatency     `json:"latency_ms"`
	TokensIn  int              `json:"tokens_in"`
	TokensOut int              `json:"tokens_out"`
	Cost      CostBreakdown    `json:"cost"`
	Request   RequestEnvelope  `json:"request"`
	Response  ResponseEnvelope `json:"response"`
	Error     string           `json:"error,omitempty"`
	Streaming bool             `json:"streaming,omitempty"`
	Shadow    *ShadowMetadata  `json:"shadow,omitempty"`
	// AutoRouted marks a call that arrived via model:"auto" — the bandit
	// reward plugin (RFC Gap 12) only trains on these; a caller who named a
	// model explicitly didn't opt into exploration.
	AutoRouted bool `json:"auto_routed,omitempty"`

	// FallbackUsed reports whether the call was served by a fallback model
	// after the originally-requested model (FallbackFrom) failed — e.g. via
	// context-window escalation. Both are zero-value on the common path.
	FallbackUsed bool   `json:"fallback_used,omitempty"`
	FallbackFrom string `json:"fallback_from,omitempty"`
}

// ShadowMetadata is attached to CallEvents emitted for shadow-routed requests.
type ShadowMetadata struct {
	PrimaryID    string `json:"primary_id,omitempty"`
	PrimaryModel string `json:"primary_model,omitempty"`
	ShadowModel  string `json:"shadow_model,omitempty"`
}

// DeltaChunk is one streaming delta for stream-mode CallEvents on the bus.
type DeltaChunk struct {
	Index   int    `json:"index"`
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
}

// RequestEnvelope is ixr's canonical representation of an inbound chat request.
type RequestEnvelope struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  any       `json:"tool_choice,omitempty"` // string | ToolChoiceObject
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	N           int       `json:"n,omitempty"`
	Stop        any       `json:"stop,omitempty"` // string | []string
	User        string    `json:"user,omitempty"`
}

// ResponseEnvelope is ixr's canonical representation of a chat response,
// shaped to match the OpenAI response format so existing SDKs just work.
type ResponseEnvelope struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Message is a single turn in a conversation. Content is the plain-text
// shape; Parts is populated instead when the caller sent a multi-part
// "content" array (text + images) — see MarshalJSON/UnmarshalJSON in
// content.go. A message never has both populated by the unmarshaler,
// though Content is also flattened from Parts' text blocks for callers
// that only read Content.
type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"-"` // marshaled under "content" instead of Content when non-empty; see content.go
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // set on role="tool" messages; identifies which ToolCall this responds to
	Name       string        `json:"name,omitempty"`         // optional function name on role="tool" messages
}

// Choice is one completion candidate in the response.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage reports token consumption for a call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
