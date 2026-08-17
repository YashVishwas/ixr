package compressor

import (
	"context"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestIntercept_NeverBlocks(t *testing.T) {
	p := New(0)
	req := &schema.RequestEnvelope{Messages: []schema.Message{{Role: "user", Content: "hi"}}}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestIntercept_BelowThreshold_NoOp(t *testing.T) {
	p := New(4000)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "what's the weather?"},
			{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "t1"}}},
			{Role: "tool", ToolCallID: "t1", Content: "72F and sunny"},
		},
	}
	orig := make([]schema.Message, len(req.Messages))
	copy(orig, req.Messages)

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range req.Messages {
		if req.Messages[i].Content != orig[i].Content {
			t.Errorf("message %d: content changed for short content: got %q, want %q", i, req.Messages[i].Content, orig[i].Content)
		}
	}
}

func TestIntercept_NeverTouchesSystemPrompt(t *testing.T) {
	p := New(10) // deliberately tiny threshold
	longSystem := strings.Repeat("be very concise and helpful. ", 50)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "system", Content: longSystem},
			{Role: "user", Content: "hi"},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Messages[0].Content != longSystem {
		t.Errorf("system prompt was modified despite being oversized: got %q", req.Messages[0].Content)
	}
}

func TestIntercept_NeverTouchesLatestUserTurn(t *testing.T) {
	p := New(10) // deliberately tiny threshold
	longQuestion := strings.Repeat("please help me understand this in great detail. ", 20)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "user", Content: "old question"},
			{Role: "assistant", Content: "old answer"},
			{Role: "user", Content: longQuestion},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Messages[2].Content != longQuestion {
		t.Errorf("latest user turn was modified: got %q", req.Messages[2].Content)
	}
	// Older user turns are fair game for compression (unlike the live turn
	// above) — with this test's deliberately tiny threshold, that message is
	// expected to be truncated, which is exactly the point of the contrast.
	if req.Messages[0].Content == "old question" {
		t.Errorf("expected the older turn to be compressed under a tiny threshold, got it unchanged")
	}
}

func TestIntercept_SkipsMultimodalMessages(t *testing.T) {
	p := New(10)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "tool", ToolCallID: "t1", Parts: []schema.ContentPart{{Type: "text", Text: strings.Repeat("x", 100)}}},
			{Role: "user", Content: "describe this image"},
		},
	}
	orig := req.Messages[0].Parts[0].Text
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Messages[0].Parts[0].Text != orig {
		t.Errorf("multimodal message part was modified, expected untouched")
	}
}

func TestIntercept_CompressesOversizedToolResult(t *testing.T) {
	p := New(50)
	longResult := strings.Repeat("data ", 50) // 250 chars, over the 50-char threshold
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "user", Content: "old question"},
			{Role: "tool", ToolCallID: "t1", Content: longResult},
			{Role: "user", Content: "new question"},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Messages[1].Content
	if len(got) >= len(longResult) {
		t.Errorf("expected compression to shrink content: got len=%d, original len=%d", len(got), len(longResult))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected a truncation marker, got %q", got)
	}
}

// --- compress (byte-for-byte) ---

func TestCompress_BelowThreshold_ReturnsCollapsedButUntruncated(t *testing.T) {
	in := "line one\nline two"
	got := compress(in, 1000)
	want := "line one\nline two"
	if got != want {
		t.Errorf("compress(%q) = %q, want %q", in, got, want)
	}
}

func TestCompress_TruncatesPastMaxChars(t *testing.T) {
	in := strings.Repeat("a", 100)
	got := compress(in, 10)
	want := strings.Repeat("a", 10) + "\n... [truncated 90 chars]"
	if got != want {
		t.Errorf("compress(%q, 10) = %q, want %q", in, got, want)
	}
}

// --- collapseBlankLines (byte-for-byte) ---

func TestCollapseBlankLines_TrimsTrailingWhitespacePerLine(t *testing.T) {
	in := "hello   \nworld\t\t\n"
	got := collapseBlankLines(in)
	want := "hello\nworld\n"
	if got != want {
		t.Errorf("collapseBlankLines(%q) = %q, want %q", in, got, want)
	}
}

func TestCollapseBlankLines_CollapsesConsecutiveBlankLines(t *testing.T) {
	in := "a\n\n\n\n\nb"
	got := collapseBlankLines(in)
	want := "a\n\nb"
	if got != want {
		t.Errorf("collapseBlankLines(%q) = %q, want %q", in, got, want)
	}
}

func TestCollapseBlankLines_NoBlankLines_Unchanged(t *testing.T) {
	in := "a\nb\nc"
	got := collapseBlankLines(in)
	if got != in {
		t.Errorf("collapseBlankLines(%q) = %q, want unchanged", in, got)
	}
}

// --- collapseRepeatedLines (byte-for-byte) ---

func TestCollapseRepeatedLines_CollapsesRunsOfThreeOrMore(t *testing.T) {
	in := "start\nerror: timeout\nerror: timeout\nerror: timeout\nerror: timeout\nend"
	got := collapseRepeatedLines(in)
	want := "start\nerror: timeout\n... (repeated 4 times)\nend"
	if got != want {
		t.Errorf("collapseRepeatedLines(%q) = %q, want %q", in, got, want)
	}
}

func TestCollapseRepeatedLines_LeavesRunsOfTwoAlone(t *testing.T) {
	in := "a\nb\nb\nc"
	got := collapseRepeatedLines(in)
	if got != in {
		t.Errorf("collapseRepeatedLines(%q) = %q, want unchanged (run of 2 is below the collapse threshold)", in, got)
	}
}

func TestCollapseRepeatedLines_DoesNotCollapseRepeatedBlankLines(t *testing.T) {
	// collapseBlankLines is responsible for blank-line runs; this function
	// should leave them alone even if called directly on un-collapsed input.
	in := "a\n\n\n\nb"
	got := collapseRepeatedLines(in)
	if got != in {
		t.Errorf("collapseRepeatedLines(%q) = %q, want unchanged (blank-line runs are collapseBlankLines' job)", in, got)
	}
}

// --- lastUserMessageIndex ---

func TestLastUserMessageIndex(t *testing.T) {
	cases := []struct {
		name     string
		messages []schema.Message
		want     int
	}{
		{"no messages", nil, -1},
		{"no user message", []schema.Message{{Role: "system", Content: "x"}}, -1},
		{"single user message", []schema.Message{{Role: "user", Content: "x"}}, 0},
		{"user then assistant", []schema.Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}}, 0},
		{"multiple user turns", []schema.Message{
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
			{Role: "user", Content: "3"},
		}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lastUserMessageIndex(c.messages); got != c.want {
				t.Errorf("lastUserMessageIndex(%+v) = %d, want %d", c.messages, got, c.want)
			}
		})
	}
}

func TestName(t *testing.T) {
	if got := New(0).Name(); got != "request-compressor" {
		t.Errorf("Name() = %q, want %q", got, "request-compressor")
	}
}
