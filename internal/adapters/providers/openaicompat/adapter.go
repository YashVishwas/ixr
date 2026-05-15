// Package openaicompat implements pkg/provider.Provider for OpenAI-compatible
// HTTP APIs (chat completions). Used by DeepSeek, Groq/Llama, and similar hosts.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

// New returns an adapter with the given logical provider name and default base URL
// (e.g. https://api.deepseek.com/v1). Pass baseURL="" to use defaultBase.
func New(name, apiKey, baseURL, defaultBase string) *Adapter {
	return NewWithHeaders(name, apiKey, baseURL, defaultBase, nil)
}

// NewWithHeaders is like New but sends extra HTTP headers on each request (e.g. OpenRouter).
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

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
