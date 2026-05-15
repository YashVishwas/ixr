package googleai

import (
	"fmt"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Google Generative Language API (Gemini / Gemma) wire types.

type genWireRequest struct {
	SystemInstruction *genSystemInstruction `json:"systemInstruction,omitempty"`
	Contents          []genContent          `json:"contents"`
}

type genSystemInstruction struct {
	Parts []genPart `json:"parts"`
}

type genContent struct {
	Role  string    `json:"role"`
	Parts []genPart `json:"parts"`
}

type genPart struct {
	Text string `json:"text"`
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

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemChunks = append(systemChunks, strings.TrimSpace(m.Content))
			}
		case "user", "assistant":
			appendOrMerge(m.Role, m.Content)
		default:
			appendOrMerge("user", m.Content)
		}
	}

	out := genWireRequest{Contents: contents}
	if len(systemChunks) > 0 {
		out.SystemInstruction = &genSystemInstruction{
			Parts: []genPart{{Text: strings.Join(systemChunks, "\n\n")}},
		}
	}
	return out
}

func fromGenWireResponse(model string, wr *genWireResponse) (*schema.ResponseEnvelope, error) {
	if wr.PromptFeedback != nil && wr.PromptFeedback.BlockReason != "" {
		return nil, fmt.Errorf("googleai: prompt blocked (%s)", wr.PromptFeedback.BlockReason)
	}
	if len(wr.Candidates) == 0 {
		return nil, fmt.Errorf("googleai: no candidates in response")
	}

	c0 := wr.Candidates[0]
	text, err := extractText(c0.Content.Parts)
	if err != nil {
		return nil, err
	}

	return &schema.ResponseEnvelope{
		ID:      "",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []schema.Choice{
			{
				Index:        0,
				Message:      schema.Message{Role: "assistant", Content: text},
				FinishReason: mapFinishReason(c0.FinishReason),
			},
		},
		Usage: schema.Usage{
			PromptTokens:     wr.UsageMetadata.PromptTokenCount,
			CompletionTokens: wr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      wr.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func extractText(parts []genPart) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("googleai: no text in candidate content")
	}
	return b.String(), nil
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
