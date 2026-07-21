package ingress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// writeSSEHeader sets headers required for server-sent events.
func writeSSEHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

type sseChunkEnvelope struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []sseDeltaChoice `json:"choices"`
}

type sseDeltaChoice struct {
	Index        int     `json:"index"`
	Delta        sseDelta `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type sseDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []schema.ToolCall `json:"tool_calls,omitempty"`
}

// writeSSEChunk converts a provider.StreamChunk to the OpenAI SSE format and writes it.
func writeSSEChunk(w http.ResponseWriter, chunk provider.StreamChunk) error {
	var finishReason *string
	if chunk.FinishReason != "" {
		fr := chunk.FinishReason
		finishReason = &fr
	}
	env := sseChunkEnvelope{
		ID:      chunk.ID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   chunk.Model,
		Choices: []sseDeltaChoice{{
			Index:        0,
			Delta:        sseDelta{Role: chunk.Delta.Role, Content: chunk.Delta.Content, ToolCalls: chunk.Delta.ToolCalls},
			FinishReason: finishReason,
		}},
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// writeSSEDone writes the terminal [DONE] event.
func writeSSEDone(w http.ResponseWriter) {
	fmt.Fprint(w, "data: [DONE]\n\n")
}
