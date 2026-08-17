package googleai

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Google Generative Language API (Gemini / Gemma) wire types.

type genWireRequest struct {
	SystemInstruction *genSystemInstruction `json:"systemInstruction,omitempty"`
	Contents          []genContent          `json:"contents"`
	Tools             []genTool             `json:"tools,omitempty"`
	ToolConfig        *genToolConfig        `json:"toolConfig,omitempty"`
}

type genSystemInstruction struct {
	Parts []genPart `json:"parts"`
}

type genContent struct {
	Role  string    `json:"role"`
	Parts []genPart `json:"parts"`
}

// genPart is a one-of: exactly one of Text, FunctionCall, FunctionResponse,
// InlineData, or FileData is set, matching Gemini's part shape.
type genPart struct {
	Text             string               `json:"text,omitempty"`
	FunctionCall     *genFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *genFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *genInlineData       `json:"inlineData,omitempty"`
	FileData         *genFileData         `json:"fileData,omitempty"`
}

// genInlineData is Gemini's inline-base64 image part.
type genInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// genFileData is Gemini's URL-referenced image part — Gemini fetches it
// itself, the same role a "url"-type source plays in Anthropic's/Bedrock's
// image content blocks.
type genFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

type genFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type genFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type genTool struct {
	FunctionDeclarations []genFunctionDeclaration `json:"functionDeclarations"`
}

type genFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// genToolConfig mirrors Gemini's
// {"toolConfig":{"functionCallingConfig":{"mode":"AUTO"|"ANY"|"NONE"}}}.
type genToolConfig struct {
	FunctionCallingConfig genFunctionCallingConfig `json:"functionCallingConfig"`
}

type genFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type genWireResponse struct {
	Candidates     []genCandidate `json:"candidates"`
	UsageMetadata  genUsage       `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
}

type genCandidate struct {
	Content      genContent `json:"content"`
	FinishReason string     `json:"finishReason"`
}

type genUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func toGenWireRequest(req *schema.RequestEnvelope) genWireRequest {
	var systemChunks []string
	var contents []genContent
	// Gemini's functionResponse only carries a name, not a call ID, so a
	// tool-role message (which carries ixr's synthesized ToolCallID) needs
	// this to look the name back up.
	callIDToName := map[string]string{}

	appendOrMerge := func(role, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		genRole := role
		if genRole == "assistant" {
			genRole = "model"
		}
		if len(contents) > 0 && contents[len(contents)-1].Role == genRole {
			last := &contents[len(contents)-1]
			last.Parts[0].Text = strings.TrimSpace(last.Parts[0].Text + "\n" + text)
			return
		}
		contents = append(contents, genContent{
			Role:  genRole,
			Parts: []genPart{{Text: text}},
		})
	}

	var pendingResponses []genPart
	flushResponses := func() {
		if len(pendingResponses) > 0 {
			contents = append(contents, genContent{Role: "function", Parts: pendingResponses})
			pendingResponses = nil
		}
	}

	for _, m := range req.Messages {
		if m.Role == "tool" {
			name := m.Name
			if name == "" {
				name = callIDToName[m.ToolCallID]
			}
			pendingResponses = append(pendingResponses, genPart{
				FunctionResponse: &genFunctionResponse{
					Name:     name,
					Response: toResponseObject(m.Content),
				},
			})
			continue
		}
		flushResponses()

		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemChunks = append(systemChunks, strings.TrimSpace(m.Content))
			}
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var parts []genPart
				if strings.TrimSpace(m.Content) != "" {
					parts = append(parts, genPart{Text: m.Content})
				}
				for i, tc := range m.ToolCalls {
					id := tc.ID
					if id == "" {
						id = "fc_" + strconv.Itoa(i)
					}
					callIDToName[id] = tc.Function.Name
					var args map[string]any
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
					parts = append(parts, genPart{FunctionCall: &genFunctionCall{Name: tc.Function.Name, Args: args}})
				}
				contents = append(contents, genContent{Role: "model", Parts: parts})
				continue
			}
			appendOrMerge("assistant", m.Content)
		case "user":
			if len(m.Parts) > 0 {
				// Multimodal — build a dedicated content entry with one
				// part per Part (text and/or image) rather than routing
				// through appendOrMerge, which only handles a single text
				// string and would merge this into a plain-text neighbor.
				contents = append(contents, genContent{Role: "user", Parts: toGenParts(m)})
				continue
			}
			appendOrMerge("user", m.Content)
		default:
			appendOrMerge("user", m.Content)
		}
	}
	flushResponses()

	out := genWireRequest{
		Contents: contents,
		Tools:    toGenTools(req.Tools),
	}
	if len(systemChunks) > 0 {
		out.SystemInstruction = &genSystemInstruction{
			Parts: []genPart{{Text: strings.Join(systemChunks, "\n\n")}},
		}
	}
	return out
}

// toGenParts converts a multimodal message's Parts into Gemini parts: a
// text part per "text" Part, an inline-base64 or file-URI image part per
// "image_url" Part. Mirrors the Anthropic/Bedrock adapters'
// toWireContentBlocks/toContentBlocks, since all three ultimately serve the
// same canonical schema.ContentPart shape.
func toGenParts(m schema.Message) []genPart {
	parts := make([]genPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				parts = append(parts, genPart{Text: p.Text})
			}
		case "image_url":
			if p.ImageURL != nil {
				parts = append(parts, toImagePart(p.ImageURL.URL))
			}
		}
	}
	return parts
}

// toImagePart builds a Gemini image part from an image_url part's URL: a
// data: URI decodes into an inline base64 part (mime type + payload
// extracted directly, no re-encoding); any other URL is passed as a
// fileData part, which Gemini fetches itself.
func toImagePart(url string) genPart {
	if mimeType, data, ok := parseImageDataURI(url); ok {
		return genPart{InlineData: &genInlineData{MimeType: mimeType, Data: data}}
	}
	return genPart{FileData: &genFileData{FileURI: url}}
}

// parseImageDataURI extracts the mime type and base64 payload from a
// "data:image/png;base64,AAAA..." URI.
func parseImageDataURI(uri string) (mimeType, data string, ok bool) {
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

func toGenTools(tools []schema.Tool) []genTool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]genFunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = genFunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
	}
	return []genTool{{FunctionDeclarations: decls}}
}

// toResponseObject wraps a tool result string as the object Gemini's
// functionResponse.response field requires — passing the parsed object
// through directly if the content is already a JSON object, otherwise
// wrapping the raw string.
func toResponseObject(content string) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err == nil {
		return obj
	}
	return map[string]any{"result": content}
}

// synthCallID assigns a stable per-response ID to a function call, since
// Gemini itself doesn't return one — it matches responses back to calls by
// name, not ID, but pkg/schema.ToolCall requires an ID for OpenAI-shaped
// clients that key off it.
func synthCallID(name string, index int) string {
	return fmt.Sprintf("fc_%s_%d", name, index)
}

func fromGenWireResponse(model string, wr *genWireResponse) (*schema.ResponseEnvelope, error) {
	if wr.PromptFeedback != nil && wr.PromptFeedback.BlockReason != "" {
		return nil, fmt.Errorf("googleai: prompt blocked (%s)", wr.PromptFeedback.BlockReason)
	}
	if len(wr.Candidates) == 0 {
		return nil, fmt.Errorf("googleai: no candidates in response")
	}

	c0 := wr.Candidates[0]
	text, calls := splitParts(c0.Content.Parts)

	finish := mapFinishReason(c0.FinishReason)
	if len(calls) > 0 {
		finish = "tool_calls"
	}

	return &schema.ResponseEnvelope{
		ID:      "",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []schema.Choice{
			{
				Index: 0,
				Message: schema.Message{
					Role:      "assistant",
					Content:   text,
					ToolCalls: calls,
				},
				FinishReason: finish,
			},
		},
		Usage: schema.Usage{
			PromptTokens:     wr.UsageMetadata.PromptTokenCount,
			CompletionTokens: wr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      wr.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

// splitParts separates a candidate's text and functionCall parts. A
// function-call-only response (no text part) is valid and yields an empty
// string, not an error.
func splitParts(parts []genPart) (string, []schema.ToolCall) {
	var b strings.Builder
	var calls []schema.ToolCall
	for i, p := range parts {
		if p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
		if p.FunctionCall != nil {
			args, _ := json.Marshal(p.FunctionCall.Args)
			calls = append(calls, schema.ToolCall{
				ID:   synthCallID(p.FunctionCall.Name, i),
				Type: "function",
				Function: schema.ToolFunction{
					Name:      p.FunctionCall.Name,
					Arguments: string(args),
				},
			})
		}
	}
	return b.String(), calls
}

func mapFinishReason(reason string) string {
	switch strings.ToUpper(reason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "OTHER":
		return "content_filter"
	default:
		if reason == "" {
			return "stop"
		}
		return strings.ToLower(reason)
	}
}
