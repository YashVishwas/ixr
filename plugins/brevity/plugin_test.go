package brevity

import (
	"context"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestIntercept_NeverBlocks(t *testing.T) {
	p := New("")
	req := &schema.RequestEnvelope{Messages: []schema.Message{{Role: "user", Content: "hi"}}}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestIntercept_AppendsToExistingSystemMessage(t *testing.T) {
	p := New("BE TERSE")
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "hi"},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected message count unchanged (2), got %d", len(req.Messages))
	}
	want := "You are a helpful assistant.\n\nBE TERSE"
	if req.Messages[0].Content != want {
		t.Errorf("system message: got %q, want %q", req.Messages[0].Content, want)
	}
	if req.Messages[1].Content != "hi" {
		t.Errorf("user message must be untouched, got %q", req.Messages[1].Content)
	}
}

func TestIntercept_CreatesSystemMessageWhenNoneExists(t *testing.T) {
	p := New("BE TERSE")
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{{Role: "user", Content: "hi"}},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected a new system message prepended (2 total), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "BE TERSE" {
		t.Errorf("expected a new system message %q, got %+v", "BE TERSE", req.Messages[0])
	}
	if req.Messages[1].Content != "hi" {
		t.Errorf("original message must still be present, got %+v", req.Messages[1])
	}
}

func TestIntercept_UsesFirstSystemMessageOnly(t *testing.T) {
	p := New("BE TERSE")
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "system", Content: "first"},
			{Role: "system", Content: "second"},
			{Role: "user", Content: "hi"},
		},
	}
	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Messages[0].Content != "first\n\nBE TERSE" {
		t.Errorf("first system message: got %q", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "second" {
		t.Errorf("second system message must be untouched, got %q", req.Messages[1].Content)
	}
}

func TestNew_EmptyInstructionUsesDefault(t *testing.T) {
	p := New("")
	if p.instruction != defaultInstruction {
		t.Errorf("expected defaultInstruction, got %q", p.instruction)
	}
	if !strings.Contains(p.instruction, "Preserve code blocks") {
		t.Errorf("default instruction should preserve code/commands/errors verbatim, got %q", p.instruction)
	}
}

func TestName(t *testing.T) {
	if got := New("").Name(); got != "brevity" {
		t.Errorf("Name() = %q, want %q", got, "brevity")
	}
}

// --- appendInstruction (byte-for-byte) ---

func TestAppendInstruction_EmptyExisting(t *testing.T) {
	got := appendInstruction("", "BE TERSE")
	if got != "BE TERSE" {
		t.Errorf("appendInstruction(\"\", ...) = %q, want %q", got, "BE TERSE")
	}
}

func TestAppendInstruction_TrimsTrailingNewlines(t *testing.T) {
	got := appendInstruction("existing\n\n\n", "BE TERSE")
	want := "existing\n\nBE TERSE"
	if got != want {
		t.Errorf("appendInstruction = %q, want %q", got, want)
	}
}

// --- systemMessageIndex ---

func TestSystemMessageIndex(t *testing.T) {
	cases := []struct {
		name     string
		messages []schema.Message
		want     int
	}{
		{"no messages", nil, -1},
		{"no system message", []schema.Message{{Role: "user", Content: "x"}}, -1},
		{"system first", []schema.Message{{Role: "system", Content: "x"}, {Role: "user", Content: "y"}}, 0},
		{"system not first", []schema.Message{{Role: "user", Content: "x"}, {Role: "system", Content: "y"}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := systemMessageIndex(c.messages); got != c.want {
				t.Errorf("systemMessageIndex(%+v) = %d, want %d", c.messages, got, c.want)
			}
		})
	}
}
