package openaicompat

import "github.com/YashVishwas/ixr/pkg/schema"

// OpenAI-compatible chat completions wire types.

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Tools       []schema.Tool `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
}

// wireMessage's tool fields reuse schema.ToolCall directly: the OpenAI wire
// format for tool_calls (id/type/function.name/function.arguments) is
// exactly what pkg/schema already models. Content is `any` since the wire
// format is polymorphic (string or content-part array); schema.ContentPart
// matches the array shape exactly, so it round-trips with no translation.
type wireMessage struct {
	Role       string            `json:"role"`
	Content    any               `json:"content,omitempty"`
	ToolCalls  []schema.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

// toWireContent picks the multimodal array shape when the message has
// content parts, otherwise the plain string (or nil, so omitempty drops it
// for assistant messages that only carry tool_calls).
func toWireContent(m schema.Message) any {
	if len(m.Parts) > 0 {
		return m.Parts
	}
	if m.Content == "" {
		return nil
	}
	return m.Content
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

// wireDeltaResponse is the JSON shape of each SSE chunk from OpenAI-compat APIs.
type wireDeltaResponse struct {
	ID      string            `json:"id"`
	Model   string            `json:"model"`
	Choices []wireDeltaChoice `json:"choices"`
	Usage   *wireDeltaUsage   `json:"usage,omitempty"`
}

type wireDeltaChoice struct {
	Index        int              `json:"index"`
	Delta        wireDeltaMessage `json:"delta"`
	FinishReason string           `json:"finish_reason"`
}

type wireDeltaMessage struct {
	Role      string              `json:"role,omitempty"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []wireDeltaToolCall `json:"tool_calls,omitempty"`
}

// wireDeltaToolCall is a fragment of a tool call spread across multiple SSE
// chunks. Index identifies which tool call (of possibly several parallel
// calls) this fragment belongs to; ID/Name arrive once on the first
// fragment, Arguments arrives incrementally and must be concatenated.
type wireDeltaToolCall struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function wireDeltaToolFunction `json:"function,omitempty"`
}

type wireDeltaToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type wireDeltaUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func toWireRequest(req *schema.RequestEnvelope) wireRequest {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = wireMessage{
			Role:       m.Role,
			Content:    toWireContent(m),
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

func fromWireResponse(wr *wireResponse) *schema.ResponseEnvelope {
	choices := make([]schema.Choice, len(wr.Choices))
	for i, c := range wr.Choices {
		content, _ := c.Message.Content.(string)
		choices[i] = schema.Choice{
			Index: c.Index,
			Message: schema.Message{
				Role:      c.Message.Role,
				Content:   content,
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
