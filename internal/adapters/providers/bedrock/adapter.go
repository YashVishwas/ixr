// Package bedrock implements pkg/provider.Provider for AWS Bedrock.
// Uses raw HTTP with SigV4 signing — no AWS SDK dependency required.
// Supported model families: Anthropic Claude, Meta Llama, Amazon Nova/Titan.
package bedrock

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Adapter calls AWS Bedrock's InvokeModel API using raw SigV4-signed HTTP.
type Adapter struct {
	region    string
	accessKey string
	secretKey string
	sessionToken string
	client    *http.Client
}

// New creates a Bedrock adapter.
// region: e.g. "us-east-1". Pass sessionToken="" for long-term credentials.
func New(region, accessKey, secretKey, sessionToken string) *Adapter {
	return &Adapter{
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		sessionToken: sessionToken,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *Adapter) Name() string { return "bedrock" }

func (a *Adapter) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	body, err := a.buildBody(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock: build body: %w", err)
	}

	url := a.endpointURL(req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if err := a.sign(httpReq, body); err != nil {
		return nil, fmt.Errorf("bedrock: sign: %w", err)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bedrock: status %d: %s", resp.StatusCode, respBody)
	}

	return a.parseResponse(req.Model, respBody)
}

// Stream is not yet implemented for Bedrock — falls back to a Chat call.
func (a *Adapter) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	resp, err := a.Chat(ctx, req)
	if err != nil {
		return err
	}
	if len(resp.Choices) > 0 {
		_ = fn(provider.StreamChunk{
			ID:           resp.ID,
			Model:        resp.Model,
			Delta:        schema.Message{Role: "assistant", Content: resp.Choices[0].Message.Content},
			FinishReason: resp.Choices[0].FinishReason,
			Usage:        &resp.Usage,
		})
	}
	return nil
}

func (a *Adapter) endpointURL(model string) string {
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", a.region, model)
}

// buildBody converts the canonical request to the Bedrock Claude wire format.
// This handles the Anthropic Claude family hosted on Bedrock — the same
// wire shape as native Anthropic's Messages API, including a top-level
// "system" string separate from "messages".
// Other families (Llama, Nova) use different schemas — extend as needed.
func (a *Adapter) buildBody(req *schema.RequestEnvelope) ([]byte, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type bedrockReq struct {
		AnthropicVersion string    `json:"anthropic_version"`
		MaxTokens        int       `json:"max_tokens"`
		System           string    `json:"system,omitempty"`
		Messages         []message `json:"messages"`
	}

	var system string
	msgs := make([]message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			// Lifted to the top-level System field below, not sent as a
			// message — Bedrock's Claude endpoint rejects role="system" in
			// the messages array, same as native Anthropic. Concatenate
			// rather than overwrite: a request can legitimately carry more
			// than one system-role message (e.g. MemoryMiddleware prepends
			// a user-facts system message ahead of the caller's own one) —
			// this used to silently drop every system message, always, via
			// an unconditional `continue` with no System field to lift into
			// at all.
			if system != "" {
				system += "\n\n" + m.Content
			} else {
				system = m.Content
			}
			continue
		}
		if len(m.Parts) > 0 {
			// Vision (RFC Gap 10) isn't wired for this adapter yet — only
			// OpenAI, openaicompat, and Anthropic translate m.Parts today.
			// m.Content already carries the flattened text (see
			// pkg/schema/content.go's UnmarshalJSON), so the request still
			// goes through as text-only rather than erroring or sending a
			// malformed body — but silently, with no signal to the caller
			// that the image was dropped. Surface that here so it's at
			// least visible to the operator instead of a mystery "why did
			// the model ignore my image" support ticket.
			slog.Warn("bedrock: message has image/multipart content but this adapter only forwards text; image dropped", "role", m.Role)
		}
		msgs = append(msgs, message{Role: m.Role, Content: m.Content})
	}
	return json.Marshal(bedrockReq{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        4096,
		System:           system,
		Messages:         msgs,
	})
}

// parseResponse handles the Anthropic Claude Bedrock response format.
func (a *Adapter) parseResponse(model string, body []byte) (*schema.ResponseEnvelope, error) {
	var wire struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("bedrock: decode response: %w", err)
	}

	var content string
	for _, c := range wire.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	return &schema.ResponseEnvelope{
		ID:      wire.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []schema.Choice{{
			Index:        0,
			Message:      schema.Message{Role: "assistant", Content: content},
			FinishReason: wire.StopReason,
		}},
		Usage: schema.Usage{
			PromptTokens:     wire.Usage.InputTokens,
			CompletionTokens: wire.Usage.OutputTokens,
			TotalTokens:      wire.Usage.InputTokens + wire.Usage.OutputTokens,
		},
	}, nil
}

// ── SigV4 signing ──────────────────────────────────────────────────────────────

func (a *Adapter) sign(req *http.Request, body []byte) error {
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", datetime)
	if a.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", a.sessionToken)
	}

	bodyHash := hashHex(body)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)

	// Canonical request
	signedHeaders, canonicalHeaders := buildCanonicalHeaders(req)
	canonicalURI := canonicalizeURI(req.URL.Path)
	canonicalQuery := req.URL.RawQuery

	canonicalReq := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")

	// String to sign
	credScope := date + "/bedrock/" + a.region + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + credScope + "\n" + hashHex([]byte(canonicalReq))

	// Signing key
	sigKey := hmacHex(
		hmacBytes(hmacBytes(hmacBytes(hmacBytes([]byte("AWS4"+a.secretKey), date), a.region), "bedrock"), "aws4_request"),
		stringToSign,
	)

	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		a.accessKey, credScope, signedHeaders, sigKey,
	)
	req.Header.Set("Authorization", auth)
	return nil
}

func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacBytes(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func hmacHex(key []byte, data string) string {
	return hex.EncodeToString(hmacBytes(key, data))
}

func buildCanonicalHeaders(req *http.Request) (signedHeaders, canonicalHeaders string) {
	headers := map[string]string{
		"content-type":        req.Header.Get("Content-Type"),
		"host":                req.Host,
		"x-amz-content-sha256": req.Header.Get("X-Amz-Content-Sha256"),
		"x-amz-date":          req.Header.Get("X-Amz-Date"),
	}
	if tok := req.Header.Get("X-Amz-Security-Token"); tok != "" {
		headers["x-amz-security-token"] = tok
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var hdrLines []string
	for _, k := range keys {
		hdrLines = append(hdrLines, k+":"+strings.TrimSpace(headers[k]))
	}
	return strings.Join(keys, ";"), strings.Join(hdrLines, "\n") + "\n"
}

func canonicalizeURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
