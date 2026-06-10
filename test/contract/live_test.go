//go:build live

// Package contract contains nightly live-provider contract tests.
// Run with: go test -tags live -timeout 60s ./test/contract/...
// Requires real API keys in environment (OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.).
package contract

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/adapters/providers/anthropic"
	"github.com/YashVishwas/ixr/internal/adapters/providers/cerebras"
	"github.com/YashVishwas/ixr/internal/adapters/providers/openai"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var helloReq = &schema.RequestEnvelope{
	Messages: []schema.Message{{Role: "user", Content: "Say exactly: pong"}},
}

func ctx() context.Context {
	c, _ := context.WithTimeout(context.Background(), 30*time.Second)
	return c
}

func TestOpenAI_Chat(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	req := *helloReq
	req.Model = "gpt-4o-mini"
	p := openai.New(key, "")
	resp, err := p.Chat(ctx(), &req)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}
	t.Logf("response: %s", resp.Choices[0].Message.Content)
}

func TestOpenAI_Stream(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	req := *helloReq
	req.Model = "gpt-4o-mini"
	p := openai.New(key, "")
	var chunks int
	err := p.Stream(ctx(), &req, func(_ provider.StreamChunk) error {
		chunks++
		return nil
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if chunks == 0 {
		t.Fatal("received zero stream chunks")
	}
	t.Logf("received %d chunks", chunks)
}

func TestAnthropic_Chat(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	req := *helloReq
	req.Model = "claude-haiku-4-5"
	p := anthropic.New(key, "")
	resp, err := p.Chat(ctx(), &req)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}
	t.Logf("response: %s", resp.Choices[0].Message.Content)
}

func TestCerebras_Chat(t *testing.T) {
	key := os.Getenv("CEREBRAS_API_KEY")
	if key == "" {
		t.Skip("CEREBRAS_API_KEY not set")
	}
	req := *helloReq
	req.Model = "llama-4-scout-17b-16e-instruct"
	p := cerebras.New(key, "")
	resp, err := p.Chat(ctx(), &req)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}
	t.Logf("response: %s", resp.Choices[0].Message.Content)
}
