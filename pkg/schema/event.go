// Package schema contains the public data contracts for ixr.
// Everything in this package is semver-governed; third parties build on these types.
package schema

import "time"

// CallEvent is emitted on the bus for every LLM call that passes through ixr.
// It is the primary unit of data the intelligence layer consumes.
type CallEvent struct {
	ID        string           `json:"id"`
	Timestamp time.Time        `json:"timestamp"`
	UseCaseID string           `json:"use_case_id"` // from X-IXR-UseCase header
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
	LatencyMS int64            `json:"latency_ms"`
	TokensIn  int              `json:"tokens_in"`
	TokensOut int              `json:"tokens_out"`
	Cost      CostBreakdown    `json:"cost"`
	Request   RequestEnvelope  `json:"request"`
	Response  ResponseEnvelope `json:"response"`
	Error     string           `json:"error,omitempty"`
	Shadow    *ShadowMetadata  `json:"shadow,omitempty"`
	Chain     *ChainMeta       `json:"chain,omitempty"`
}

// ShadowMetadata identifies events produced by shadow routing. Shadow responses
// are emitted for telemetry and offline comparison, never returned to callers.
type ShadowMetadata struct {
	PrimaryID    string `json:"primary_id,omitempty"`
	PrimaryModel string `json:"primary_model,omitempty"`
	ShadowModel  string `json:"shadow_model,omitempty"`
}

// ChainMeta is attached to each CallEvent that is part of a multi-model chain.
// It lets consumers correlate all stages of a single chain invocation.
type ChainMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Stage       int    `json:"stage"`
	TotalStages int    `json:"total_stages"`
}

// RequestEnvelope is ixr's canonical representation of an inbound chat request.
type RequestEnvelope struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
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

// Message is a single turn in a conversation.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
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
