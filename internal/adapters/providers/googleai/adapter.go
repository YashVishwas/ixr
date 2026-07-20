// Package googleai implements pkg/provider.Provider for the Google AI
// Generative Language API (Gemini and Gemma models on the free AI Studio tier).
package googleai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com"

// Adapter implements pkg/provider.Provider for Gemini/Gemma generateContent.
type Adapter struct {
	providerName string
	apiKey       string
	baseURL      string
	client       *http.Client
}

// NewGemini returns an adapter with Name() "gemini".
func NewGemini(apiKey, baseURL string) *Adapter {
	return newAdapter("gemini", apiKey, baseURL)
}

// NewGemma returns an adapter with Name() "gemma".
func NewGemma(apiKey, baseURL string) *Adapter {
	return newAdapter("gemma", apiKey, baseURL)
}

func newAdapter(providerName, apiKey, baseURL string) *Adapter {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Adapter{
		providerName: providerName,
		apiKey:       apiKey,
		baseURL:      strings.TrimRight(baseURL, "/"),
		client:       &http.Client{},
	}
}

func (a *Adapter) Name() string { return a.providerName }

// Chat sends req to :generateContent and returns a normalised response.
func (a *Adapter) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	wire := toGenWireRequest(req)
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", a.providerName, err)
	}

	apiURL := fmt.Sprintf(
		"%s/v1beta/models/%s:generateContent?key=%s",
		a.baseURL,
		url.PathEscape(req.Model),
		url.QueryEscape(a.apiKey),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", a.providerName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: do request: %w", a.providerName, err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read response body: %w", a.providerName, err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d: %s", a.providerName, httpResp.StatusCode, respBody)
	}

	var wireResp genWireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", a.providerName, err)
	}

	return fromGenWireResponse(req.Model, &wireResp)
}

// Stream sends req to :streamGenerateContent?alt=sse and delivers each chunk to fn.
// Google streams a sequence of full GenerateContentResponse JSON objects as SSE data lines.
func (a *Adapter) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	wire := toGenWireRequest(req)
	body, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("%s: marshal stream request: %w", a.providerName, err)
	}

	apiURL := fmt.Sprintf(
		"%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s",
		a.baseURL,
		url.PathEscape(req.Model),
		url.QueryEscape(a.apiKey),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: build stream request: %w", a.providerName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%s: do stream request: %w", a.providerName, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("%s: stream status %d: %s", a.providerName, httpResp.StatusCode, b)
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

		var wr genWireResponse
		if err := json.Unmarshal([]byte(data), &wr); err != nil {
			continue
		}
		if len(wr.Candidates) == 0 {
			continue
		}
		c0 := wr.Candidates[0]
		// Gemini streams complete GenerateContentResponse objects per chunk
		// (not fragments), so a functionCall part is already whole here —
		// no cross-chunk accumulation needed, unlike OpenAI/Anthropic.
		text, calls := splitParts(c0.Content.Parts)

		u := schema.Usage{
			PromptTokens:     wr.UsageMetadata.PromptTokenCount,
			CompletionTokens: wr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      wr.UsageMetadata.TotalTokenCount,
		}
		var usagePtr *schema.Usage
		if wr.UsageMetadata.TotalTokenCount > 0 {
			usagePtr = &u
		}

		finish := mapFinishReason(c0.FinishReason)
		if len(calls) > 0 {
			finish = "tool_calls"
		}
		chunk := provider.StreamChunk{
			Model:        req.Model,
			Delta:        schema.Message{Role: "assistant", Content: text, ToolCalls: calls},
			FinishReason: finish,
			Usage:        usagePtr,
		}
		if err := fn(chunk); err != nil {
			return err
		}
	}
	return scanner.Err()
}
