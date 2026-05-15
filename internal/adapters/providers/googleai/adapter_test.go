package googleai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestAdapter_Chat_HappyPath(t *testing.T) {
	fixture := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{map[string]any{"text": "hello"}},
					"role":  "model",
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     3,
			"candidatesTokenCount": 1,
			"totalTokenCount":      4,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") == "" {
			t.Error("expected key query param")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	a := NewGemini("test-key", srv.URL)
	req := &schema.RequestEnvelope{
		Model:    "gemini-2.0-flash",
		Messages: []schema.Message{{Role: "user", Content: "hi"}},
	}
	resp, err := a.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Errorf("content: got %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 4 {
		t.Errorf("tokens: got %d", resp.Usage.TotalTokens)
	}
	if a.Name() != "gemini" {
		t.Errorf("name: got %q", a.Name())
	}
}

func TestNewGemma_Name(t *testing.T) {
	a := NewGemma("k", "")
	if a.Name() != "gemma" {
		t.Errorf("name: got %q", a.Name())
	}
}
