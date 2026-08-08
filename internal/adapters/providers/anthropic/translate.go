package anthropic

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Anthropic Messages API wire types — internal to this adapter.

type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	// System is the array-of-content-blocks form. Anthropic also accepts a
	// bare string, but the array form is required to attach cache_control
	// to it — see maybeCacheSystemBlock.
	System     []wireContent   `json:"system,omitempty"`
	MaxTokens  int             `json:"max_tokens"`
	Stream     bool            `json:"stream,omitempty"`
	Tools      []wireTool      `json:"tools,omitempty"`
	ToolChoice *wireToolChoice `json:"tool_choice,omitempty"`
}

// wireMessage.Content is always a content-block array. Anthropic accepts
// this form for plain text too, which keeps one representation for text,
// tool_use, and tool_result turns instead of a string/array union type.
type wireMessage struct {
	Role    string        `json:"role"`
	Content []wireContent `json:"content"`
}

// wireContent is Anthropic's single content-block shape, reused across
// requests and responses: "text" (Text), "tool_use" (ID/Name/Input, emitted
// by the model), "tool_result" (ToolUseID/Content, sent back to the model
// as the outcome of a tool call), and "image" (Source, sent to the model).
type wireContent struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     map[string]any   `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   string           `json:"content,omitempty"`
	IsError   bool             `json:"is_error,omitempty"`
	Source    *wireImageSource `json:"source,omitempty"`
	// CacheControl marks this block as a prompt-cache breakpoint: Anthropic
	// bills a repeated prefix ending at this block ~90% cheaper on
	// subsequent calls that reuse it. Only set on the system block today —
	// see maybeCacheSystemBlock.
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

// wireCacheControl is Anthropic's prompt-caching marker.
type wireCacheControl struct {
	Type string `json:"type"` // "ephemeral" — the only type Anthropic currently defines
}

// wireImageSource is Anthropic's image content-block source: either
// inline base64 bytes or a URL Anthropic fetches itself.
type wireImageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// wireTool is Anthropic's native tool definition shape — flat, unlike
// OpenAI's {type, function: {...}} wrapper.
type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// wireToolChoice mirrors Anthropic's {"type": "auto"|"any"|"tool", "name"}.
type wireToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type wireResponse struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Role       string        `json:"role"`
	Content    []wireContent `json:"content"`
	Model      string        `json:"model"`
	StopReason string        `json:"stop_reason"`
	Usage      wireUsage     `json:"usage"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

const defaultMaxTokens = 4096

// wireRequest adds the stream flag for streaming calls.
func (w wireRequest) withStream() wireRequest {
	w.Stream = true
	return w
}

// SSE stream wire types for the Anthropic streaming Messages API.

type streamMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID    string    `json:"id"`
		Model string    `json:"model"`
		Usage wireUsage `json:"usage"`
	} `json:"message"`
}

// streamContentBlockStart announces a new content block at Index. For
// type=="tool_use" it carries the tool call's ID/Name up front; the
// argument JSON itself streams in afterward via content_block_delta.
type streamContentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
}

type streamContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"`
		// Text carries text_delta content; PartialJSON carries
		// input_json_delta fragments for a tool_use block — concatenating
		// them across the stream yields the complete arguments JSON.
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
}

type streamMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// toWireRequest converts ixr's canonical envelope to the Anthropic Messages API format.
// System messages are lifted out of the messages array into the top-level system field,
// as required by the Anthropic API. Anthropic has no role="tool" — consecutive tool
// result messages are coalesced into a single role="user" message with one
// tool_result content block per result, which the API requires.
//
// historyLen is the number of leading req.Messages entries that are
// SessionMiddleware-injected history from prior turns rather than this
// turn's new content (see internal/domain/cache.WithHistoryLen and
// internal/ingress/session_middleware.go) — 0 when there's no session or
// no history yet. When positive, the last message within that prefix gets
// a cache_control breakpoint (see markHistoryCacheBreakpoint) so a growing
// multi-turn conversation's stable prefix is cached across turns, not just
// the system prompt.
func toWireRequest(req *schema.RequestEnvelope, historyLen int) wireRequest {
	var system string
	var msgs []wireMessage
	var pendingResults []wireContent

	flushResults := func() {
		if len(pendingResults) > 0 {
			msgs = append(msgs, wireMessage{Role: "user", Content: pendingResults})
			pendingResults = nil
		}
	}

	for _, m := range req.Messages {
		if m.Role == "tool" {
			pendingResults = append(pendingResults, wireContent{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
			continue
		}
		flushResults()

		if m.Role == "system" {
			system = m.Content
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []wireContent
			if m.Content != "" {
				blocks = append(blocks, wireContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				blocks = append(blocks, wireContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			msgs = append(msgs, wireMessage{Role: "assistant", Content: blocks})
			continue
		}

		msgs = append(msgs, wireMessage{
			Role:    m.Role,
			Content: toWireContentBlocks(m),
		})
	}
	flushResults()

	markHistoryCacheBreakpoint(msgs, historyLen, req.Model)

	var systemBlocks []wireContent
	if system != "" {
		systemBlocks = []wireContent{maybeCacheSystemBlock(req.Model, system)}
	}

	return wireRequest{
		Model:      req.Model,
		Messages:   msgs,
		System:     systemBlocks,
		MaxTokens:  defaultMaxTokens,
		Tools:      toWireTools(req.Tools),
		ToolChoice: toWireToolChoice(req.ToolChoice),
	}
}

// minCacheableTokens is Anthropic's minimum prompt length eligible for
// cache_control: 2048 tokens for Haiku models, 1024 for everything else
// (Sonnet, Opus). Marking a shorter block wastes the write (Anthropic still
// bills the initial cache write at a premium) for no future benefit.
func minCacheableTokens(model string) int {
	if strings.Contains(strings.ToLower(model), "haiku") {
		return 2048
	}
	return 1024
}

// estimateTokens approximates token count using the same rule of thumb
// already used elsewhere in ixr (internal/ingress/session_middleware.go):
// 1 token ≈ 4 characters.
func estimateTokens(text string) int {
	return len(text) / 4
}

// maybeCacheSystemBlock marks the system prompt as a cache_control
// breakpoint when it's long enough to clear Anthropic's minimum — a
// repeated system prompt (or static RAG/tool-def context baked into it) is
// then billed at ~10% of normal input cost on every subsequent call that
// reuses it, even when the final user turn is novel. Below the minimum,
// caching would just add a wasted cache-write premium with no reuse benefit.
func maybeCacheSystemBlock(model, system string) wireContent {
	block := wireContent{Type: "text", Text: system}
	if estimateTokens(system) >= minCacheableTokens(model) {
		block.CacheControl = &wireCacheControl{Type: "ephemeral"}
	}
	return block
}

// markHistoryCacheBreakpoint marks the last message within the
// session-injected history prefix as a cache_control breakpoint, so a
// growing multi-turn conversation's history is cached across turns the same
// way the system prompt already is — this is what Anthropic (and Headroom,
// under the name "live-zone compression") mean by caching everything except
// the newest turn.
//
// Assumes the first historyLen entries of msgs correspond 1:1, in order, to
// the first historyLen entries of req.Messages — true for
// SessionMiddleware's actual output (internal/ingress/session_middleware.go
// only ever stores clean [user, assistant] pairs, never system or tool
// messages, so nothing in that prefix triggers toWireRequest's tool-result
// coalescing or system-message extraction). If a caller bypasses
// SessionMiddleware and hand-assembles multi-turn history into one request,
// this assumption can break down — the result is a suboptimal cache split
// (wrong boundary, or marking a tool-coalesced block), not a correctness
// bug: cache_control placement has no effect on what's actually sent to the
// model, only on cost/latency.
//
// plugins/compressor can never invalidate this: it runs before
// SessionMiddleware injects history (see that package's doc), so it never
// sees or touches session-stored messages at all — only the caller's new
// turn for the current request.
func markHistoryCacheBreakpoint(msgs []wireMessage, historyLen int, model string) {
	if historyLen <= 0 || historyLen > len(msgs) {
		return
	}

	var totalChars int
	for _, m := range msgs[:historyLen] {
		for _, c := range m.Content {
			totalChars += len(c.Text) + len(c.Content)
		}
	}
	if totalChars/4 < minCacheableTokens(model) {
		return
	}

	blocks := msgs[historyLen-1].Content
	if len(blocks) == 0 {
		return
	}
	blocks[len(blocks)-1].CacheControl = &wireCacheControl{Type: "ephemeral"}
}

// toWireContentBlocks converts a message's content into Anthropic content
// blocks: a single text block for a plain-text message (today's shape,
// unchanged), or one block per part for a multimodal message.
func toWireContentBlocks(m schema.Message) []wireContent {
	if len(m.Parts) == 0 {
		return []wireContent{{Type: "text", Text: m.Content}}
	}
	blocks := make([]wireContent, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, wireContent{Type: "text", Text: p.Text})
		case "image_url":
			if p.ImageURL != nil {
				blocks = append(blocks, toImageBlock(p.ImageURL.URL))
			}
		}
	}
	return blocks
}

// toImageBlock builds an Anthropic image content block from an image_url
// part's URL: a data: URI decodes into an inline base64 source (media type
// + payload extracted directly, no re-encoding); any other URL is passed
// as a "url" source, which Anthropic fetches itself.
func toImageBlock(url string) wireContent {
	if mediaType, data, ok := parseDataURI(url); ok {
		return wireContent{Type: "image", Source: &wireImageSource{Type: "base64", MediaType: mediaType, Data: data}}
	}
	return wireContent{Type: "image", Source: &wireImageSource{Type: "url", URL: url}}
}

// parseDataURI extracts the media type and base64 payload from a
// "data:image/png;base64,AAAA..." URI.
func parseDataURI(uri string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := uri[len(prefix):]
	const marker = ";base64,"
	idx := strings.Index(rest, marker)
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+len(marker):], true
}

func toWireTools(tools []schema.Tool) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, len(tools))
	for i, t := range tools {
		out[i] = wireTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		}
	}
	return out
}

// toWireToolChoice maps OpenAI-shaped tool_choice (a bare string, or a
// {"type":"function","function":{"name":...}} object — RequestEnvelope
// carries it as `any` since it comes straight off the JSON-decoded
// request) onto Anthropic's {"type":"auto"|"any"|"tool","name":...}.
func toWireToolChoice(choice any) *wireToolChoice {
	switch v := choice.(type) {
	case string:
		switch v {
		case "required":
			return &wireToolChoice{Type: "any"}
		case "auto":
			return &wireToolChoice{Type: "auto"}
		default: // "none" or anything else — let tools/,omitempty govern
			return nil
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				return &wireToolChoice{Type: "tool", Name: name}
			}
		}
	}
	return nil
}

// fromWireResponse converts an Anthropic response to ixr's canonical envelope.
func fromWireResponse(wr *wireResponse) (*schema.ResponseEnvelope, error) {
	text, toolCalls := splitContent(wr.Content)

	finish := normalizeStopReason(wr.StopReason)

	return &schema.ResponseEnvelope{
		ID:      wr.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   wr.Model,
		Choices: []schema.Choice{
			{
				Index: 0,
				Message: schema.Message{
					Role:      "assistant",
					Content:   text,
					ToolCalls: toolCalls,
				},
				FinishReason: finish,
			},
		},
		Usage: usageFromWire(wr.Usage),
	}, nil
}

// usageFromWire converts Anthropic's usage shape to ixr's canonical Usage.
// Anthropic reports input_tokens as only the non-cached portion of the
// prompt — cache_creation_input_tokens and cache_read_input_tokens are
// separate, additive counts — so PromptTokens must sum all three to mean
// the same thing it does for every other provider: the full prompt token
// count.
func usageFromWire(u wireUsage) schema.Usage {
	promptTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	return schema.Usage{
		PromptTokens:             promptTokens,
		CompletionTokens:         u.OutputTokens,
		TotalTokens:              promptTokens + u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
}

// splitContent separates a response's text and tool_use blocks. A
// tool-only response (no text block) is valid and yields an empty string,
// not an error — the caller drives on ToolCalls/FinishReason instead.
func splitContent(content []wireContent) (string, []schema.ToolCall) {
	var text string
	var calls []schema.ToolCall
	for _, c := range content {
		switch c.Type {
		case "text":
			text += c.Text
		case "tool_use":
			args, _ := json.Marshal(c.Input)
			calls = append(calls, schema.ToolCall{
				ID:   c.ID,
				Type: "function",
				Function: schema.ToolFunction{
					Name:      c.Name,
					Arguments: string(args),
				},
			})
		}
	}
	return text, calls
}

// streamToolCallAccumulator reassembles tool_use content blocks streamed as
// content_block_start (ID/Name) + content_block_delta input_json_delta
// fragments (Arguments, concatenated — Anthropic guarantees the
// concatenation of partial_json fragments is valid JSON) into complete
// schema.ToolCall values, preserving block order.
type streamToolCallAccumulator struct {
	byIndex map[int]*schema.ToolCall
	order   []int
}

func newStreamToolCallAccumulator() *streamToolCallAccumulator {
	return &streamToolCallAccumulator{byIndex: map[int]*schema.ToolCall{}}
}

func (a *streamToolCallAccumulator) start(index int, id, name string) {
	tc := &schema.ToolCall{ID: id, Type: "function", Function: schema.ToolFunction{Name: name}}
	a.byIndex[index] = tc
	a.order = append(a.order, index)
}

func (a *streamToolCallAccumulator) appendArgs(index int, partialJSON string) {
	if tc, ok := a.byIndex[index]; ok {
		tc.Function.Arguments += partialJSON
	}
}

func (a *streamToolCallAccumulator) empty() bool { return len(a.order) == 0 }

func (a *streamToolCallAccumulator) finalize() []schema.ToolCall {
	out := make([]schema.ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		out = append(out, *a.byIndex[idx])
	}
	return out
}

// normalizeStopReason maps Anthropic stop reasons to OpenAI finish reasons
// so callers that branch on finish_reason don't need provider-specific logic.
func normalizeStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}
