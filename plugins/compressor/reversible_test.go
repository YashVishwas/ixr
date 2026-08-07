package compressor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/retrieval"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// fakeStore is a minimal, order-preserving stand-in for *retrieval.Store —
// records every Put call so tests can assert on exactly what was stored,
// not just that something was.
type fakeStore struct {
	puts []struct {
		content string
		ttl     time.Duration
	}
	nextID int
}

func (f *fakeStore) Put(_ context.Context, content string, ttl time.Duration) string {
	f.nextID++
	f.puts = append(f.puts, struct {
		content string
		ttl     time.Duration
	}{content, ttl})
	return fmt.Sprintf("ret_test_%d", f.nextID)
}

func TestReversible_TruncationStoresOriginalAndAppendsMarker(t *testing.T) {
	store := &fakeStore{}
	p := NewReversible(10, store, time.Minute)
	original := strings.Repeat("a", 100)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "user", Content: "hi"}, // deliberately short — must not itself exceed the 10-char threshold
			{Role: "tool", ToolCallID: "t1", Content: original},
			{Role: "user", Content: "new question"},
		},
	}

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.puts) != 1 {
		t.Fatalf("expected exactly one Put call, got %d", len(store.puts))
	}
	if store.puts[0].content != original {
		t.Errorf("expected the original, uncompressed content to be stored, got %q", store.puts[0].content)
	}
	if store.puts[0].ttl != time.Minute {
		t.Errorf("ttl: got %v, want %v", store.puts[0].ttl, time.Minute)
	}

	got := req.Messages[1].Content
	if !strings.Contains(got, retrieval.ToolName) {
		t.Errorf("expected a retrieval marker referencing %s, got %q", retrieval.ToolName, got)
	}
}

func TestReversible_InjectsRetrieveToolOnlyOnce(t *testing.T) {
	store := &fakeStore{}
	p := NewReversible(10, store, time.Minute)
	longA := strings.Repeat("a", 100)
	longB := strings.Repeat("b", 100)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "tool", ToolCallID: "t1", Content: longA},
			{Role: "tool", ToolCallID: "t2", Content: longB},
			{Role: "user", Content: "question"},
		},
	}

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.puts) != 2 {
		t.Fatalf("expected two Put calls (both messages truncated), got %d", len(store.puts))
	}
	count := 0
	for _, tool := range req.Tools {
		if tool.Function.Name == retrieval.ToolName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one retrieve tool definition regardless of how many messages were truncated, got %d", count)
	}
}

func TestReversible_NoToolInjectedWhenNothingTruncated(t *testing.T) {
	store := &fakeStore{}
	p := NewReversible(4000, store, time.Minute) // threshold high enough nothing truncates
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "tool", ToolCallID: "t1", Content: "short result"},
			{Role: "user", Content: "question"},
		},
	}

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.puts) != 0 {
		t.Errorf("expected no Put calls when nothing was truncated, got %d", len(store.puts))
	}
	if len(req.Tools) != 0 {
		t.Errorf("expected no tools injected when nothing was truncated, got %+v", req.Tools)
	}
}

func TestReversible_StreamingRequest_FallsBackToDestructive(t *testing.T) {
	store := &fakeStore{}
	p := NewReversible(10, store, time.Minute)
	original := strings.Repeat("a", 100)
	req := &schema.RequestEnvelope{
		Stream: true,
		Messages: []schema.Message{
			{Role: "tool", ToolCallID: "t1", Content: original},
			{Role: "user", Content: "question"},
		},
	}

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Still compressed (destructive mode is always active)...
	if len(req.Messages[0].Content) >= len(original) {
		t.Errorf("expected content to still be compressed for a streaming request")
	}
	// ...but no store write and no synthetic tool, since there's no
	// resolution path for a tool call mid-stream.
	if len(store.puts) != 0 {
		t.Errorf("expected no Put calls for a streaming request, got %d", len(store.puts))
	}
	if len(req.Tools) != 0 {
		t.Errorf("expected no retrieve tool injected for a streaming request, got %+v", req.Tools)
	}
	if strings.Contains(req.Messages[0].Content, retrieval.ToolName) {
		t.Errorf("expected no retrieval marker for a streaming request, got %q", req.Messages[0].Content)
	}
}

func TestDestructiveMode_NoStoreConfigured_Unaffected(t *testing.T) {
	// New (not NewReversible) — must behave exactly as it did before this
	// feature existed: no store, no tool injection, ever.
	p := New(10)
	original := strings.Repeat("a", 100)
	req := &schema.RequestEnvelope{
		Messages: []schema.Message{
			{Role: "tool", ToolCallID: "t1", Content: original},
			{Role: "user", Content: "question"},
		},
	}

	if err := p.Intercept(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Tools) != 0 {
		t.Errorf("expected no tools injected in destructive mode, got %+v", req.Tools)
	}
	if strings.Contains(req.Messages[0].Content, retrieval.ToolName) {
		t.Errorf("expected no retrieval marker in destructive mode, got %q", req.Messages[0].Content)
	}
}
