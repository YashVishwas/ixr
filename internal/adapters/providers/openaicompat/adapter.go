// Package openaicompat implements pkg/provider.Provider for OpenAI-compatible
// HTTP APIs (chat completions). Used by DeepSeek, Groq/Llama, and similar hosts.
package openaicompat

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

// Adapter calls an OpenAI-compatible /chat/completions endpoint.
type Adapter struct {
	name         string
	apiKey       string
	baseURL      string
	extraHeaders map[string]string
	client       *http.Client
}

// New returns an adapter with the given logical provider name and default base URL.
func New(name, apiKey, baseURL, defaultBase string) *Adapter {
	return NewWithHeaders(name, apiKey, baseURL, defaultBase, nil)
}

// NewWithHeaders is like New but sends extra HTTP headers on each request.
func NewWithHeaders(name, apiKey, baseURL, defaultBase string, extraHeaders map[string]string) *Adapter {
	if baseURL == "" {
		baseURL = defaultBase
	}
	return &Adapter{
		name:         name,
		apiKey:       apiKey,
		baseURL:      baseURL,
		extraHeaders: extraHeaders,
		client:       &http.Client{},
	}
}

func (a *Adapter) Name() string { return a.name }

// Chat sends req to the provider and returns a normalised response.
func (a *Adapter) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	body, err := json.Marshal(toWireRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", a.name, err)
	}

	url := trimTrailingSlash(a.baseURL) + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", a.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	for k, v := range a.extraHeaders {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do request: %w", a.name, err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read response body: %w", a.name, err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d: %s", a.name, httpResp.StatusCode, respBody)
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", a.name, err)
	}

	return fromWireResponse(&wireResp), nil
}

// Stream sends req with stream=true and delivers each SSE chunk to fn.
func (a *Adapter) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	wireReq := toWireRequest(req)
	wireReq.Stream = true

	body, err := json.Marshal(wireReq)
	if err != nil {
		return fmt.Errorf("%s: marshal stream request: %w", a.name, err)
	}

	url := trimTrailingSlash(a.baseURL) + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: build stream request: %w", a.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range a.extraHeaders {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s: do stream request: %w", a.name, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("%s: stream status %d: %s", a.name, httpResp.StatusCode, b)
	}

	toolCalls := newToolCallAccumulator()
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
			continue // malformed chunk — skip
		}
		if len(delta.Choices) > 0 {
			toolCalls.add(delta.Choices[0].Delta.ToolCalls)
		}
		chunk := deltaToChunk(&delta)
		// Tool call arguments arrive fragmented across many chunks; only
		// the finish chunk carries the fully reassembled calls.
		if chunk.FinishReason != "" && !toolCalls.empty() {
			chunk.Delta.ToolCalls = toolCalls.finalize()
		}
		if err := fn(chunk); err != nil {
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
			PromptTokens:         d.Usage.PromptTokens,
			CompletionTokens:     d.Usage.CompletionTokens,
			TotalTokens:          d.Usage.TotalTokens,
			CacheReadInputTokens: d.Usage.cachedTokens(),
		}
		chunk.Usage = &u
	}
	return chunk
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
