package anthropic

import (
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// --- markHistoryCacheBreakpoint (unit, byte-for-byte on the msgs slice) ---

func TestMarkHistoryCacheBreakpoint_MarksLastMessageInPrefix(t *testing.T) {
	long := strings.Repeat("a ", 2500) // 5000 chars / ~1250 tokens, over the 1024-token threshold on its own
	msgs := []wireMessage{
		{Role: "user", Content: []wireContent{{Type: "text", Text: long}}},
		{Role: "assistant", Content: []wireContent{{Type: "text", Text: "reply"}}},
		{Role: "user", Content: []wireContent{{Type: "text", Text: "new question"}}},
	}
	markHistoryCacheBreakpoint(msgs, 2, "claude-3-5-sonnet-20241022")

	if msgs[1].Content[0].CacheControl == nil {
		t.Fatalf("expected the last history message (index 1) to be marked, got %+v", msgs[1])
	}
	if msgs[0].Content[0].CacheControl != nil {
		t.Errorf("expected earlier history messages to be untouched, got %+v", msgs[0])
	}
	if msgs[2].Content[0].CacheControl != nil {
		t.Errorf("expected the new turn (outside the history prefix) to be untouched, got %+v", msgs[2])
	}
}

func TestMarkHistoryCacheBreakpoint_BelowThreshold_NoMark(t *testing.T) {
	msgs := []wireMessage{
		{Role: "user", Content: []wireContent{{Type: "text", Text: "short"}}},
		{Role: "assistant", Content: []wireContent{{Type: "text", Text: "short reply"}}},
		{Role: "user", Content: []wireContent{{Type: "text", Text: "new question"}}},
	}
	markHistoryCacheBreakpoint(msgs, 2, "claude-3-5-sonnet-20241022")

	for i, m := range msgs {
		if m.Content[0].CacheControl != nil {
			t.Errorf("message %d: expected no cache_control below the token threshold, got marked", i)
		}
	}
}

func TestMarkHistoryCacheBreakpoint_ZeroHistoryLen_NoMark(t *testing.T) {
	long := strings.Repeat("a ", 1000)
	msgs := []wireMessage{
		{Role: "user", Content: []wireContent{{Type: "text", Text: long}}},
	}
	markHistoryCacheBreakpoint(msgs, 0, "claude-3-5-sonnet-20241022")

	if msgs[0].Content[0].CacheControl != nil {
		t.Errorf("expected no mark when historyLen=0, got marked")
	}
}

func TestMarkHistoryCacheBreakpoint_HistoryLenExceedsMessages_NoPanicNoMark(t *testing.T) {
	msgs := []wireMessage{
		{Role: "user", Content: []wireContent{{Type: "text", Text: "only one message"}}},
	}
	markHistoryCacheBreakpoint(msgs, 5, "claude-3-5-sonnet-20241022") // out of bounds

	if msgs[0].Content[0].CacheControl != nil {
		t.Errorf("expected no mark when historyLen exceeds len(msgs), got marked")
	}
}

func TestMarkHistoryCacheBreakpoint_EmptyContentBlock_NoPanic(t *testing.T) {
	msgs := []wireMessage{
		{Role: "assistant", Content: nil}, // e.g. a tool_use-only turn stored with an empty text block set — defensive case
	}
	// Must not panic even though minCacheableTokens will likely not be
	// reached here anyway; the real guard under test is the len(blocks)==0 check.
	markHistoryCacheBreakpoint(msgs, 1, "claude-3-5-sonnet-20241022")
}

// --- toWireRequest integration ---

func TestToWireRequest_MultiTurnHistory_CachesStablePrefix(t *testing.T) {
	long := strings.Repeat("Let's discuss the architecture in detail. ", 120) // ~5000 chars / ~1250 tokens, over threshold
	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "user", Content: long},
			{Role: "assistant", Content: "Sure, here's an overview..."},
			{Role: "user", Content: "what's next"}, // this turn's new content
		},
	}

	got := toWireRequest(req, 2) // first 2 messages are session-injected history

	if len(got.Messages) != 3 {
		t.Fatalf("messages: got %d, want 3", len(got.Messages))
	}
	if got.Messages[1].Content[0].CacheControl == nil {
		t.Errorf("expected the last history message to carry cache_control, got %+v", got.Messages[1])
	}
	if got.Messages[2].Content[0].CacheControl != nil {
		t.Errorf("expected the new turn to be untouched, got %+v", got.Messages[2])
	}
}

func TestToWireRequest_NoHistory_NoBreakpointMarked(t *testing.T) {
	req := &schema.RequestEnvelope{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{{Role: "user", Content: "hello"}},
	}
	got := toWireRequest(req, 0)
	if got.Messages[0].Content[0].CacheControl != nil {
		t.Errorf("expected no cache_control with historyLen=0, got marked")
	}
}
