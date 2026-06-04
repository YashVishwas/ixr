package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Adapter implements pkg/provider.Provider for OpenAI.
type Adapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New creates an Adapter using the given API key.
// baseURL is overridable for testing; pass "" to use the default.
func New(apiKey, baseURL string) *Adapter {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Adapter{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

func (a *Adapter) Name() string { return "openai" }

// Chat sends req to OpenAI and returns the normalized response.
func (a *Adapter) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	body, err := json.Marshal(toWireRequest(req))
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: status %d: %s", httpResp.StatusCode, respBody)
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}

	return fromWireResponse(&wireResp), nil
}

// Stream sends req with stream=true and delivers each SSE chunk to fn.
func (a *Adapter) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	wireReq := toWireRequest(req)
	wireReq.Stream = true

	body, err := json.Marshal(wireReq)
	if err != nil {
		return fmt.Errorf("openai: marshal stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai: do stream request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("openai: stream status %d: %s", httpResp.StatusCode, b)
	}

	scanner := bufio.NewScanner(httpResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return nil
		}
		var delta wireDeltaResponse
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			continue
		}
		if err := fn(deltaToChunk(&delta)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func deltaToChunk(d *wireDeltaResponse) provider.StreamChunk {
	chunk := provider.StreamChunk{ID: d.ID, Model: d.Model}
	if len(d.Choices) > 0 {
		c := d.Choices[0]
		chunk.Delta = schema.Message{Role: c.Delta.Role, Content: c.Delta.Content}
		chunk.FinishReason = c.FinishReason
	}
	if d.Usage != nil {
		u := schema.Usage{
			PromptTokens:     d.Usage.PromptTokens,
			CompletionTokens: d.Usage.CompletionTokens,
			TotalTokens:      d.Usage.TotalTokens,
		}
		chunk.Usage = &u
	}
	return chunk
}
