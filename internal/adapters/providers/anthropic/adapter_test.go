package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestAdapter_Chat_HappyPath(t *testing.T) {
	fixture := wireResponse{
		ID:    "msg-abc",
		Model: "claude-3-5-sonnet-20241022",
		Content: []wireContent{
			{Type: "text", Text: "hello from claude"},
		},
		StopReason: "end_turn",
		Usage:      wireUsage{InputTokens: 6, OutputTokens: 4},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("anthropic-version: got %q, want %q", r.Header.Get("anthropic-version"), anthropicVersion)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	a := New("test-key", srv.URL)
	req := &schema.RequestEnvelope{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{{Role: "user", Content: "hi"}},
	}

	resp, err := a.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices: got %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "hello from claude" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want stop", resp.Choices[0].FinishReason)
	}
}

// TestAdapter_Chat_ReadsHistoryLenFromContext confirms Chat actually reads
// cache.HistoryLenFromContext and threads it into toWireRequest — not just
// that toWireRequest itself works (already covered in history_cache_test.go)
// but that the wiring from context to the real HTTP request body is live.
func TestAdapter_Chat_ReadsHistoryLenFromContext(t *testing.T) {
	long := strings.Repeat("a ", 2500) // over the cache threshold on its own
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{
			ID:         "msg-abc",
			Content:    []wireContent{{Type: "text", Text: "ok"}},
			StopReason: "end_turn",
		})
	}))
	defer srv.Close()

	a := New("test-key", srv.URL)
	req := &schema.RequestEnvelope{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{
			{Role: "user", Content: long},
			{Role: "assistant", Content: "ack"},
			{Role: "user", Content: "new question"},
		},
	}

	ctx := cache.WithHistoryLen(context.Background(), 2)
	if _, err := a.Chat(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sent wireRequest
	if err := json.Unmarshal(capturedBody, &sent); err != nil {
		t.Fatalf("decode captured request body: %v", err)
	}
	if len(sent.Messages) < 2 || sent.Messages[1].Content[0].CacheControl == nil {
		t.Errorf("expected the actual HTTP request body to carry cache_control on the history boundary, got %s", capturedBody)
	}
}

func TestAdapter_Chat_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := New("bad-key", srv.URL)
	_, err := a.Chat(context.Background(), &schema.RequestEnvelope{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []schema.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}
