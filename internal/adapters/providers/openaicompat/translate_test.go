package openaicompat

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestToWireRequest_ToolsAndToolCalls(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "llama-3.3-70b-versatile",
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
		ID:    "chatcmpl-abc",
		Model: "llama-3.3-70b-versatile",
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
