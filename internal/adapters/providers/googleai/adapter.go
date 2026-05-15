// Package googleai implements pkg/provider.Provider for the Google AI
// Generative Language API (Gemini and Gemma models on the free AI Studio tier).
package googleai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
