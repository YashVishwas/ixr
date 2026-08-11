package googleai

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestToGenWireRequest_ToolsAndFunctionCalls(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "gemini-2.0-flash",
		Messages: []schema.Message{
			{Role: "user", Content: "weather in Austin?"},
			{Role: "assistant", ToolCalls: []schema.ToolCall{
				{ID: "fc_1", Type: "function", Function: schema.ToolFunction{Name: "get_weather", Arguments: `{"city":"Austin"}`}},
			}},
			{Role: "tool", ToolCallID: "fc_1", Content: `{"tempF":72}`},
		},
		Tools: []schema.Tool{{
			Type:     "function",
			Function: schema.FunctionDef{Name: "get_weather", Description: "Get current weather", Parameters: map[string]any{"type": "object"}},
		}},
	}

	got := toGenWireRequest(req)

	if len(got.Tools) != 1 || len(got.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools: got %+v", got.Tools)
	}
	if got.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Fatalf("function declaration: got %+v", got.Tools[0].FunctionDeclarations[0])
	}

	if len(got.Contents) != 3 {
		t.Fatalf("contents: got %d, want 3 (user turn, model call-turn, function response-turn)", len(got.Contents))
	}

	callTurn := got.Contents[1]
	if callTurn.Role != "model" || len(callTurn.Parts) != 1 || callTurn.Parts[0].FunctionCall == nil {
		t.Fatalf("call turn: got %+v", callTurn)
	}
	if callTurn.Parts[0].FunctionCall.Name != "get_weather" || callTurn.Parts[0].FunctionCall.Args["city"] != "Austin" {
		t.Fatalf("function call: got %+v", callTurn.Parts[0].FunctionCall)
	}

	respTurn := got.Contents[2]
	if respTurn.Role != "function" || len(respTurn.Parts) != 1 || respTurn.Parts[0].FunctionResponse == nil {
		t.Fatalf("response turn: got %+v", respTurn)
	}
	fr := respTurn.Parts[0].FunctionResponse
	// Name is recovered from the call ID -> name map since the tool
	// message itself didn't set Name.
	if fr.Name != "get_weather" {
		t.Errorf("function response name: got %q, want get_weather", fr.Name)
	}
	if fr.Response["tempF"] != float64(72) {
		t.Errorf("function response body: got %+v", fr.Response)
	}
}

// TestToGenWireRequest_ImageContentDroppedNotErroredOrCorrupted locks in
// the documented trade-off for RFC Gap 10: this adapter doesn't translate
// vision content yet, but a message carrying it must still produce a
// valid, text-only request (using the already-flattened m.Content) rather
// than erroring or sending a malformed body — see the slog.Warn next to
// this loop in translate.go for the operator-visibility half of the fix.
// TestToGenWireRequest_ImageContentTranslated is the regression test for
// RFC Gap 10's remaining half: this adapter used to silently drop image
// content (m.Parts was never translated, only the already-flattened text
// survived). Now it must produce a real inline-base64 image part.
func TestToGenWireRequest_ImageContentTranslated(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "gemini-2.0-flash",
		Messages: []schema.Message{
			{
				Role:    "user",
				Content: "what is in this image?", // flattened by pkg/schema's UnmarshalJSON
				Parts: []schema.ContentPart{
					{Type: "text", Text: "what is in this image?"},
					{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "data:image/png;base64,AAAABBBB"}},
				},
			},
		},
	}

	got := toGenWireRequest(req)

	if len(got.Contents) != 1 {
		t.Fatalf("contents: got %d, want 1", len(got.Contents))
	}
	parts := got.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts: got %d, want 2 (text + image)", len(parts))
	}
	if parts[0].Text != "what is in this image?" {
		t.Errorf("text part: got %+v", parts[0])
	}
	if parts[1].InlineData == nil {
		t.Fatalf("image part: got %+v, want InlineData set", parts[1])
	}
	if parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "AAAABBBB" {
		t.Errorf("inline data: got %+v", parts[1].InlineData)
	}
}

// TestToGenWireRequest_ImageHTTPURLPassedThrough confirms a non-data:
// image URL is sent as a fileData part (Gemini fetches it itself),
// matching the Anthropic/Bedrock adapters' equivalent "url"-type source.
func TestToGenWireRequest_ImageHTTPURLPassedThrough(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "gemini-2.0-flash",
		Messages: []schema.Message{
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "https://example.com/cat.png"}},
			}},
		},
	}

	got := toGenWireRequest(req)

	parts := got.Contents[0].Parts
	if len(parts) != 1 || parts[0].FileData == nil {
		t.Fatalf("image part: got %+v", parts)
	}
	if parts[0].FileData.FileURI != "https://example.com/cat.png" {
		t.Errorf("file data: got %+v", parts[0].FileData)
	}
}

func TestFromGenWireResponse_FunctionCallOnly(t *testing.T) {
	wr := &genWireResponse{
		Candidates: []genCandidate{{
			Content: genContent{
				Role: "model",
				Parts: []genPart{
					{FunctionCall: &genFunctionCall{Name: "get_weather", Args: map[string]any{"city": "Austin"}}},
				},
			},
			FinishReason: "STOP",
		}},
	}

	got, err := fromGenWireResponse("gemini-2.0-flash", wr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := got.Choices[0].Message
	if msg.Content != "" {
		t.Errorf("content: got %q, want empty", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls: got %+v", msg.ToolCalls)
	}
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason: got %q, want tool_calls (overridden despite raw STOP)", got.Choices[0].FinishReason)
	}
}
