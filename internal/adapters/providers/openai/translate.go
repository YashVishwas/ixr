package openai

import "github.com/YashVishwas/ixr/pkg/schema"

// OpenAI wire types — kept internal to this adapter.
// Translate between these and pkg/schema so the rest of ixr never
// knows what OpenAI's JSON looks like.

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Tools       []schema.Tool `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
}

// wireMessage's tool fields reuse schema.ToolCall directly: OpenAI's wire
// format for tool_calls (id/type/function.name/function.arguments) is
// exactly what pkg/schema already models, so no separate translation is
// needed for the non-streaming case.
type wireMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []schema.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

type wireResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   wireUsage    `json:"usage"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// wireDeltaResponse is the JSON shape of each SSE chunk.
type wireDeltaResponse struct {
	ID      string            `json:"id"`
	Model   string            `json:"model"`
	Choices []wireDeltaChoice `json:"choices"`
	Usage   *wireUsage        `json:"usage,omitempty"`
}

type wireDeltaChoice struct {
	Index        int              `json:"index"`
	Delta        wireDeltaMessage `json:"delta"`
	FinishReason string           `json:"finish_reason"`
}

type wireDeltaMessage struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []wireDeltaToolCall   `json:"tool_calls,omitempty"`
}

// wireDeltaToolCall is a fragment of a tool call spread across multiple SSE
// chunks. Index identifies which tool call (of possibly several parallel
// calls) this fragment belongs to; ID/Name arrive once on the first
// fragment, Arguments arrives incrementally and must be concatenated.
type wireDeltaToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function wireDeltaToolFunction `json:"function,omitempty"`
}

type wireDeltaToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func toWireRequest(req *schema.RequestEnvelope) wireRequest {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = wireMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
	}
	return wireRequest{
		Model:      req.Model,
		Messages:   msgs,
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
	}
}

// toolCallAccumulator reassembles tool calls streamed as index-keyed
// fragments across multiple SSE chunks (id/name arrive once, arguments
// arrive incrementally) into complete schema.ToolCall values, preserving
// the order tool calls first appeared in.
type toolCallAccumulator struct {
	byIndex map[int]*schema.ToolCall
	order   []int
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{byIndex: map[int]*schema.ToolCall{}}
}

func (a *toolCallAccumulator) add(fragments []wireDeltaToolCall) {
	for _, f := range fragments {
		tc, ok := a.byIndex[f.Index]
		if !ok {
			tc = &schema.ToolCall{}
			a.byIndex[f.Index] = tc
			a.order = append(a.order, f.Index)
		}
		if f.ID != "" {
			tc.ID = f.ID
		}
		if f.Type != "" {
			tc.Type = f.Type
		}
		if f.Function.Name != "" {
			tc.Function.Name = f.Function.Name
		}
		tc.Function.Arguments += f.Function.Arguments
	}
}

func (a *toolCallAccumulator) empty() bool { return len(a.order) == 0 }

func (a *toolCallAccumulator) finalize() []schema.ToolCall {
	out := make([]schema.ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		out = append(out, *a.byIndex[idx])
	}
	return out
}

func fromWireResponse(wr *wireResponse) *schema.ResponseEnvelope {
	choices := make([]schema.Choice, len(wr.Choices))
	for i, c := range wr.Choices {
		choices[i] = schema.Choice{
			Index: c.Index,
			Message: schema.Message{
				Role:      c.Message.Role,
				Content:   c.Message.Content,
				ToolCalls: c.Message.ToolCalls,
			},
			FinishReason: c.FinishReason,
		}
	}
	return &schema.ResponseEnvelope{
		ID:      wr.ID,
		Object:  wr.Object,
		Created: wr.Created,
		Model:   wr.Model,
		Choices: choices,
		Usage: schema.Usage{
			PromptTokens:     wr.Usage.PromptTokens,
			CompletionTokens: wr.Usage.CompletionTokens,
			TotalTokens:      wr.Usage.TotalTokens,
		},
	}
}
