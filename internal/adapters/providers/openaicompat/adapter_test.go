package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestAdapter_Chat_HappyPath(t *testing.T) {
	fixture := wireResponse{
		ID:      "chatcmpl-abc",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "deepseek-chat",
		Choices: []wireChoice{
			{Index: 0, Message: wireMessage{Role: "assistant", Content: "pong"}, FinishReason: "stop"},
		},
		Usage: wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	a := New("deepseek", "test-key", srv.URL, "unused-default")
	req := &schema.RequestEnvelope{
		Model:    "deepseek-chat",
		Messages: []schema.Message{{Role: "user", Content: "ping"}},
	}

	resp, err := a.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "deepseek" {
		t.Errorf("Name: got %q", a.Name())
	}
	if resp.Choices[0].Message.Content != "pong" {
		t.Errorf("unexpected choices: %+v", resp.Choices)
	}
}

func TestAdapter_Chat_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := New("llama", "bad-key", srv.URL, "")
	_, err := a.Chat(context.Background(), &schema.RequestEnvelope{Model: "llama-3.1-8b-instant"})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}
