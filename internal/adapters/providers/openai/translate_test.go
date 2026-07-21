package openai

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestToWireRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *schema.RequestEnvelope
		wantMsgs int
		wantModel string
	}{
		{
			name: "single user message",
			req: &schema.RequestEnvelope{
				Model:    "gpt-4o",
				Messages: []schema.Message{{Role: "user", Content: "hello"}},
			},
			wantMsgs:  1,
			wantModel: "gpt-4o",
		},
		{
			name: "system + user messages both pass through",
			req: &schema.RequestEnvelope{
				Model: "gpt-4o",
				Messages: []schema.Message{
					{Role: "system", Content: "you are helpful"},
					{Role: "user", Content: "hi"},
				},
			},
			wantMsgs:  2,
			wantModel: "gpt-4o",
		},
		{
			name:      "empty messages",
			req:       &schema.RequestEnvelope{Model: "gpt-4o"},
			wantMsgs:  0,
			wantModel: "gpt-4o",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toWireRequest(tc.req)
			if got.Model != tc.wantModel {
				t.Errorf("model: got %q, want %q", got.Model, tc.wantModel)
			}
			if len(got.Messages) != tc.wantMsgs {
				t.Errorf("messages: got %d, want %d", len(got.Messages), tc.wantMsgs)
			}
		})
	}
}

func TestToWireRequest_MultimodalContent(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "gpt-4o",
		Messages: []schema.Message{
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "text", Text: "what is this?"},
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "https://example.com/cat.png", Detail: "high"}},
			}},
		},
	}
	got := toWireRequest(req)

	parts, ok := got.Messages[0].Content.([]schema.ContentPart)
	if !ok {
		t.Fatalf("content: got %T, want []schema.ContentPart", got.Messages[0].Content)
	}
	if len(parts) != 2 || parts[1].ImageURL.URL != "https://example.com/cat.png" || parts[1].ImageURL.Detail != "high" {
		t.Errorf("parts: got %+v", parts)
	}
}

func TestToWireRequest_PlainTextContentStillString(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "gpt-4o",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}
	got := toWireRequest(req)
	if got.Messages[0].Content != "hello" {
		t.Errorf("content: got %v (%T), want plain string \"hello\"", got.Messages[0].Content, got.Messages[0].Content)
	}
}

func TestToWireRequest_ToolsAndToolCalls(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "gpt-4o",
		Messages: []schema.Message{
			{Role: "user", Content: "weather in Austin?"},
			{Role: "assistant", ToolCalls: []schema.ToolCall{
				{ID: "call_1", Type: "function", Function: schema.ToolFunction{Name: "get_weather", Arguments: `{"city":"Austin"}`}},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "72F and sunny"},
		},
		Tools: []schema.Tool{{
			Type:     "function",
			Function: schema.FunctionDef{Name: "get_weather", Description: "Get current weather"},
		}},
		ToolChoice: "auto",
	}

	got := toWireRequest(req)

	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools: got %+v", got.Tools)
	}
	if got.ToolChoice != "auto" {
		t.Errorf("tool_choice: got %v, want auto", got.ToolChoice)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages: got %d, want 3", len(got.Messages))
	}
	asst := got.Messages[1]
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant tool_calls: got %+v", asst.ToolCalls)
	}
	result := got.Messages[2]
	if result.Role != "tool" || result.ToolCallID != "call_1" || result.Content != "72F and sunny" {
		t.Fatalf("tool result message: got %+v", result)
	}
}

func TestFromWireResponse_ToolCalls(t *testing.T) {
	wr := &wireResponse{
		ID:     "chatcmpl-abc",
		Model:  "gpt-4o",
		Choices: []wireChoice{{
			Index: 0,
			Message: wireMessage{
				Role: "assistant",
				ToolCalls: []schema.ToolCall{
					{ID: "call_1", Type: "function", Function: schema.ToolFunction{Name: "get_weather", Arguments: `{"city":"Austin"}`}},
				},
			},
			FinishReason: "tool_calls",
		}},
	}

	got := fromWireResponse(wr)

	msg := got.Choices[0].Message
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls: got %+v", msg.ToolCalls)
	}
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason: got %q, want tool_calls", got.Choices[0].FinishReason)
	}
}

func TestToolCallAccumulator_ReassemblesFragmentedArguments(t *testing.T) {
	acc := newToolCallAccumulator()
	acc.add([]wireDeltaToolCall{{Index: 0, ID: "call_1", Type: "function"}})
	acc.add([]wireDeltaToolCall{{Index: 0, Function: wireDeltaToolFunction{Name: "get_weather"}}})
	acc.add([]wireDeltaToolCall{{Index: 0, Function: wireDeltaToolFunction{Arguments: `{"city"`}}})
	acc.add([]wireDeltaToolCall{{Index: 0, Function: wireDeltaToolFunction{Arguments: `:"Austin"}`}}})

	if acc.empty() {
		t.Fatal("expected non-empty accumulator")
	}
	got := acc.finalize()
	if len(got) != 1 {
		t.Fatalf("finalize: got %d calls, want 1", len(got))
	}
	if got[0].ID != "call_1" || got[0].Function.Name != "get_weather" {
		t.Fatalf("call: got %+v", got[0])
	}
	if got[0].Function.Arguments != `{"city":"Austin"}` {
		t.Errorf("arguments: got %q", got[0].Function.Arguments)
	}
}

func TestFromWireResponse(t *testing.T) {
	wr := &wireResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "gpt-4o",
		Choices: []wireChoice{
			{
				Index:        0,
				Message:      wireMessage{Role: "assistant", Content: "hello there"},
				FinishReason: "stop",
			},
		},
		Usage: wireUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	got := fromWireResponse(wr)

	if got.ID != "chatcmpl-123" {
		t.Errorf("ID: got %q, want %q", got.ID, "chatcmpl-123")
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices: got %d, want 1", len(got.Choices))
	}
	if got.Choices[0].Message.Content != "hello there" {
		t.Errorf("content: got %q, want %q", got.Choices[0].Message.Content, "hello there")
	}
	if got.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens: got %d, want 15", got.Usage.TotalTokens)
	}
}
