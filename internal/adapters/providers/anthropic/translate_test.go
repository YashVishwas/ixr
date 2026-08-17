package anthropic

import (
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestToWireRequest_SystemLifted(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "how are you"},
		},
	}

	got := toWireRequest(req)

	if len(got.System) != 1 || got.System[0].Text != "be concise" {
		t.Errorf("system: got %+v, want a single block with text %q", got.System, "be concise")
	}
	if got.System[0].CacheControl != nil {
		t.Errorf("system: expected no cache_control on a short system prompt, got %+v", got.System[0].CacheControl)
	}
	if len(got.Messages) != 3 {
		t.Errorf("messages: got %d, want 3 (system must be removed)", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("first message role: got %q, want user", got.Messages[0].Role)
	}
	if got.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens: got %d, want %d", got.MaxTokens, defaultMaxTokens)
	}
}

// TestToWireRequest_MultipleSystemMessagesConcatenated is the regression
// test for a bug found while reviewing brevity's system-message handling: a
// request can legitimately carry more than one system-role message (e.g.
// MemoryMiddleware prepends a user-facts system message ahead of the
// caller's own one) — the loop below used to overwrite `system` on every
// system-role message, silently dropping every one but the last.
func TestToWireRequest_MultipleSystemMessagesConcatenated(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "system", Content: "What you know about this user: User's name is Arun"},
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
		},
	}

	got := toWireRequest(req)

	want := "What you know about this user: User's name is Arun\n\nbe concise"
	if got.System != want {
		t.Errorf("system: got %q, want %q", got.System, want)
	}
	if len(got.Messages) != 1 {
		t.Errorf("messages: got %d, want 1 (both system messages must be removed)", len(got.Messages))
	}
}

func TestToWireRequest_NoSystem(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}

	got := toWireRequest(req)

	if len(got.System) != 0 {
		t.Errorf("system: got %+v, want empty", got.System)
	}
	if len(got.Messages) != 1 {
		t.Errorf("messages: got %d, want 1", len(got.Messages))
	}
}

func TestToWireRequest_SystemCaching(t *testing.T) {
	short := strings.Repeat("a ", 100) // well under 1024 tokens at ~4 chars/token
	long := strings.Repeat("a ", 5000) // well over 1024 tokens

	cases := []struct {
		name      string
		model     string
		system    string
		wantCache bool
	}{
		{"sonnet below threshold", "claude-3-5-sonnet-20241022", short, false},
		{"sonnet above threshold", "claude-3-5-sonnet-20241022", long, true},
		{"opus above threshold", "claude-3-opus-20240229", long, true},
		{"haiku above sonnet threshold but below haiku's own", "claude-3-5-haiku-20241022", strings.Repeat("a ", 1500), false},
		{"haiku above its own higher threshold", "claude-3-5-haiku-20241022", strings.Repeat("a ", 5000), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &schema.RequestEnvelope{
				Model: c.model,
				Messages: []schema.Message{
					{Role: "system", Content: c.system},
					{Role: "user", Content: "hello"},
				},
			}
			got := toWireRequest(req)
			if len(got.System) != 1 {
				t.Fatalf("expected exactly one system block, got %d", len(got.System))
			}
			hasCache := got.System[0].CacheControl != nil
			if hasCache != c.wantCache {
				t.Errorf("cache_control present = %v, want %v", hasCache, c.wantCache)
			}
			if hasCache && got.System[0].CacheControl.Type != "ephemeral" {
				t.Errorf("cache_control.Type = %q, want %q", got.System[0].CacheControl.Type, "ephemeral")
			}
		})
	}
}

func TestToWireRequest_MultimodalDataURI(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-sonnet-4-6",
		Messages: []schema.Message{
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "text", Text: "what is this?"},
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "data:image/png;base64,AAAABBBB"}},
			}},
		},
	}

	got := toWireRequest(req)

	if len(got.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1", len(got.Messages))
	}
	blocks := got.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content blocks: got %d, want 2", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "what is this?" {
		t.Errorf("text block: got %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("image block: got %+v", blocks[1])
	}
	if blocks[1].Source.Type != "base64" || blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "AAAABBBB" {
		t.Errorf("image source: got %+v", blocks[1].Source)
	}
}

func TestToWireRequest_MultimodalHTTPURL(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-sonnet-4-6",
		Messages: []schema.Message{
			{Role: "user", Parts: []schema.ContentPart{
				{Type: "image_url", ImageURL: &schema.ImageURLPart{URL: "https://example.com/cat.png"}},
			}},
		},
	}

	got := toWireRequest(req)

	blocks := got.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "image" || blocks[0].Source == nil {
		t.Fatalf("image block: got %+v", blocks)
	}
	if blocks[0].Source.Type != "url" || blocks[0].Source.URL != "https://example.com/cat.png" {
		t.Errorf("image source: got %+v, want a url-type source (Anthropic fetches it)", blocks[0].Source)
	}
}

func TestToWireRequest_PlainTextMessageStillSingleTextBlock(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "claude-sonnet-4-6",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}
	got := toWireRequest(req)
	blocks := got.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "hello" {
		t.Errorf("expected unchanged single-text-block shape, got %+v", blocks)
	}
}

func TestToWireRequest_Tools(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "claude-sonnet-4-6",
		Messages: []schema.Message{{Role: "user", Content: "weather?"}},
		Tools: []schema.Tool{{
			Type: "function",
			Function: schema.FunctionDef{
				Name:        "get_weather",
				Description: "Get current weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: "required",
	}

	got := toWireRequest(req)

	if len(got.Tools) != 1 || got.Tools[0].Name != "get_weather" {
		t.Fatalf("tools: got %+v", got.Tools)
	}
	if got.ToolChoice == nil || got.ToolChoice.Type != "any" {
		t.Fatalf("tool_choice: got %+v, want {any}", got.ToolChoice)
	}
}

func TestToWireRequest_ToolChoiceForcedFunction(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "claude-sonnet-4-6",
		Messages: []schema.Message{{Role: "user", Content: "hi"}},
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "get_weather"},
		},
	}

	got := toWireRequest(req)

	if got.ToolChoice == nil || got.ToolChoice.Type != "tool" || got.ToolChoice.Name != "get_weather" {
		t.Fatalf("tool_choice: got %+v, want {tool get_weather}", got.ToolChoice)
	}
}

// Anthropic has no role="tool" — an assistant tool_calls turn must become a
// tool_use content block, and the following tool result must become a
// role="user" message with a tool_result block referencing the same ID.
func TestToWireRequest_ToolCallsAndResults(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-sonnet-4-6",
		Messages: []schema.Message{
			{Role: "user", Content: "weather in Austin?"},
			{Role: "assistant", ToolCalls: []schema.ToolCall{
				{ID: "toolu_1", Type: "function", Function: schema.ToolFunction{Name: "get_weather", Arguments: `{"city":"Austin"}`}},
			}},
			{Role: "tool", ToolCallID: "toolu_1", Content: "72F and sunny"},
		},
	}

	got := toWireRequest(req)

	if len(got.Messages) != 3 {
		t.Fatalf("messages: got %d, want 3", len(got.Messages))
	}
	asst := got.Messages[1]
	if asst.Role != "assistant" || len(asst.Content) != 1 || asst.Content[0].Type != "tool_use" {
		t.Fatalf("assistant message: got %+v", asst)
	}
	if asst.Content[0].ID != "toolu_1" || asst.Content[0].Name != "get_weather" {
		t.Fatalf("tool_use block: got %+v", asst.Content[0])
	}
	if asst.Content[0].Input["city"] != "Austin" {
		t.Fatalf("tool_use input: got %+v", asst.Content[0].Input)
	}

	result := got.Messages[2]
	if result.Role != "user" || len(result.Content) != 1 || result.Content[0].Type != "tool_result" {
		t.Fatalf("tool result message: got %+v", result)
	}
	if result.Content[0].ToolUseID != "toolu_1" || result.Content[0].Content != "72F and sunny" {
		t.Fatalf("tool_result block: got %+v", result.Content[0])
	}
}

// Multiple consecutive tool-role messages (parallel tool calls) must
// coalesce into a single user message with one tool_result block each —
// Anthropic rejects them as separate messages.
func TestToWireRequest_CoalescesParallelToolResults(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model: "claude-sonnet-4-6",
		Messages: []schema.Message{
			{Role: "user", Content: "compare weather in two cities"},
			{Role: "tool", ToolCallID: "toolu_1", Content: "Austin: 72F"},
			{Role: "tool", ToolCallID: "toolu_2", Content: "Boston: 55F"},
		},
	}

	got := toWireRequest(req)

	if len(got.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2 (results coalesced)", len(got.Messages))
	}
	result := got.Messages[1]
	if len(result.Content) != 2 {
		t.Fatalf("tool_result blocks: got %d, want 2", len(result.Content))
	}
	if result.Content[0].ToolUseID != "toolu_1" || result.Content[1].ToolUseID != "toolu_2" {
		t.Fatalf("tool_result order: got %+v", result.Content)
	}
}

func TestFromWireResponse(t *testing.T) {
	wr := &wireResponse{
		ID:    "msg_123",
		Model: "claude-3-5-sonnet-20241022",
		Content: []wireContent{
			{Type: "text", Text: "hello there"},
		},
		StopReason: "end_turn",
		Usage:      wireUsage{InputTokens: 8, OutputTokens: 4},
	}

	got, err := fromWireResponse(wr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Choices) != 1 {
		t.Fatalf("choices: got %d, want 1", len(got.Choices))
	}
	if got.Choices[0].Message.Content != "hello there" {
		t.Errorf("content: got %q, want %q", got.Choices[0].Message.Content, "hello there")
	}
	if got.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want stop", got.Choices[0].FinishReason)
	}
	if got.Usage.PromptTokens != 8 {
		t.Errorf("prompt_tokens: got %d, want 8", got.Usage.PromptTokens)
	}
}

// A tool-only response (no text block) is what Anthropic returns whenever
// the model calls a tool instead of replying in text, and must not error.
func TestFromWireResponse_ToolOnlyResponse(t *testing.T) {
	wr := &wireResponse{
		Content: []wireContent{
			{Type: "tool_use", ID: "toolu_1", Name: "get_weather", Input: map[string]any{"city": "Austin"}},
		},
		StopReason: "tool_use",
	}
	got, err := fromWireResponse(wr)
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
		t.Errorf("finish_reason: got %q, want tool_calls", got.Choices[0].FinishReason)
	}
}

// TestFromWireResponse_CacheTokensCountTowardPromptTokens is the regression
// test for a bug this change would otherwise introduce: Anthropic reports
// input_tokens as only the *non-cached* portion of the prompt when
// cache_control is used — cache_read_input_tokens and
// cache_creation_input_tokens are separate, additive counts. PromptTokens
// must sum all three, or a cached call would silently under-report its
// actual prompt size relative to every other provider's PromptTokens
// meaning (and relative to what ixr actually paid for, cache discount
// aside).
func TestFromWireResponse_CacheTokensCountTowardPromptTokens(t *testing.T) {
	wr := &wireResponse{
		ID:         "msg_cached",
		Model:      "claude-3-5-sonnet-20241022",
		Content:    []wireContent{{Type: "text", Text: "hi"}},
		StopReason: "end_turn",
		Usage: wireUsage{
			InputTokens:              10, // the new, non-cached portion
			OutputTokens:             4,
			CacheReadInputTokens:     1200,
			CacheCreationInputTokens: 0,
		},
	}

	got, err := fromWireResponse(wr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const wantPrompt = 10 + 1200
	if got.Usage.PromptTokens != wantPrompt {
		t.Errorf("prompt_tokens: got %d, want %d (input + cache_read)", got.Usage.PromptTokens, wantPrompt)
	}
	if got.Usage.TotalTokens != wantPrompt+4 {
		t.Errorf("total_tokens: got %d, want %d", got.Usage.TotalTokens, wantPrompt+4)
	}
	if got.Usage.CacheReadInputTokens != 1200 {
		t.Errorf("cache_read_input_tokens: got %d, want 1200", got.Usage.CacheReadInputTokens)
	}
}

func TestNormalizeStopReason(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"tool_use", "tool_calls"},
		{"unknown_reason", "unknown_reason"},
	}
	for _, tc := range tests {
		got := normalizeStopReason(tc.in)
		if got != tc.want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
