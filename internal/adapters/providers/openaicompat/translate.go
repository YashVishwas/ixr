package openaicompat

import "github.com/YashVishwas/ixr/pkg/schema"

// OpenAI-compatible chat completions wire types.

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

func toWireRequest(req *schema.RequestEnvelope) wireRequest {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = wireMessage{Role: m.Role, Content: m.Content}
	}
	return wireRequest{Model: req.Model, Messages: msgs}
}

func fromWireResponse(wr *wireResponse) *schema.ResponseEnvelope {
	choices := make([]schema.Choice, len(wr.Choices))
	for i, c := range wr.Choices {
		choices[i] = schema.Choice{
			Index:        c.Index,
			Message:      schema.Message{Role: c.Message.Role, Content: c.Message.Content},
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
