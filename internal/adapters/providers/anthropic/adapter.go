// Package anthropic implements pkg/provider.Provider for the Anthropic Messages API.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	anthropicVersion = "2023-06-01"
)

// Adapter implements pkg/provider.Provider for Anthropic.
type Adapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New creates an Adapter using the given API key.
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

func (a *Adapter) Name() string { return "anthropic" }

func (a *Adapter) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", a.apiKey)
	r.Header.Set("anthropic-version", anthropicVersion)
}

// Chat sends req to the Anthropic Messages API and returns a normalised response.
func (a *Adapter) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	historyLen, _ := cache.HistoryLenFromContext(ctx)
	body, err := json.Marshal(toWireRequest(req, historyLen))
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: status %d: %s", httpResp.StatusCode, respBody)
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	resp, err := fromWireResponse(&wireResp)
	if err != nil {
		return nil, fmt.Errorf("anthropic: normalise response: %w", err)
	}
	return resp, nil
}

// Stream sends req with stream=true using Anthropic's multi-event SSE format.
// Sequence: message_start → content_block_delta* → message_delta → message_stop.
func (a *Adapter) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	historyLen, _ := cache.HistoryLenFromContext(ctx)
	wr := toWireRequest(req, historyLen).withStream()

	body, err := json.Marshal(wr)
	if err != nil {
		return fmt.Errorf("anthropic: marshal stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("anthropic: build stream request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("anthropic: do stream request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("anthropic: stream status %d: %s", httpResp.StatusCode, b)
	}

	var msgID, model string
	var inputTok, cacheReadTok, cacheCreationTok int
	toolCalls := newStreamToolCallAccumulator()

	scanner := bufio.NewScanner(httpResp.Body)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "message_start":
			var ms streamMessageStart
			if err := json.Unmarshal([]byte(data), &ms); err == nil {
				msgID = ms.Message.ID
				model = ms.Message.Model
				inputTok = ms.Message.Usage.InputTokens
				cacheReadTok = ms.Message.Usage.CacheReadInputTokens
				cacheCreationTok = ms.Message.Usage.CacheCreationInputTokens
			}

		case "content_block_start":
			var cbs streamContentBlockStart
			if err := json.Unmarshal([]byte(data), &cbs); err != nil || cbs.ContentBlock.Type != "tool_use" {
				continue
			}
			toolCalls.start(cbs.Index, cbs.ContentBlock.ID, cbs.ContentBlock.Name)

		case "content_block_delta":
			var cbd streamContentBlockDelta
			if err := json.Unmarshal([]byte(data), &cbd); err != nil {
				continue
			}
			switch cbd.Delta.Type {
			case "text_delta":
				chunk := provider.StreamChunk{
					ID:    msgID,
					Model: model,
					Delta: schema.Message{Role: "assistant", Content: cbd.Delta.Text},
				}
				if err := fn(chunk); err != nil {
					return err
				}
			case "input_json_delta":
				// Tool call arguments accumulate silently; the caller sees
				// them whole in the finish chunk below, same as content.
				toolCalls.appendArgs(cbd.Index, cbd.Delta.PartialJSON)
			}

		case "message_delta":
			var md streamMessageDelta
			if err := json.Unmarshal([]byte(data), &md); err != nil {
				continue
			}
			promptTokens := inputTok + cacheReadTok + cacheCreationTok
			u := schema.Usage{
				PromptTokens:             promptTokens,
				CompletionTokens:         md.Usage.OutputTokens,
				TotalTokens:              promptTokens + md.Usage.OutputTokens,
				CacheReadInputTokens:     cacheReadTok,
				CacheCreationInputTokens: cacheCreationTok,
			}
			chunk := provider.StreamChunk{
				ID:           msgID,
				Model:        model,
				FinishReason: normalizeStopReason(md.Delta.StopReason),
				Usage:        &u,
			}
			if !toolCalls.empty() {
				chunk.Delta.ToolCalls = toolCalls.finalize()
			}
			if err := fn(chunk); err != nil {
				return err
			}

		case "message_stop":
			return nil
		}
	}
	return scanner.Err()
}
