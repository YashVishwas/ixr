package ingress

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// StreamChunk is the normalized unit emitted for SSE chat streams.
type StreamChunk struct {
	ID     string `json:"id,omitempty"`
	Model  string `json:"model,omitempty"`
	Delta  string `json:"delta,omitempty"`
	Finish string `json:"finish_reason,omitempty"`
	Tokens int    `json:"tokens,omitempty"`
	Error  string `json:"error,omitempty"`
	Done   bool   `json:"done,omitempty"`
}

// StreamWriter writes OpenAI-compatible server-sent events.
type StreamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewStreamWriter prepares w for text/event-stream output.
func NewStreamWriter(w http.ResponseWriter) (*StreamWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &StreamWriter{w: w, flusher: flusher}, true
}

// WriteChunk writes one SSE data event.
func (s *StreamWriter) WriteChunk(chunk StreamChunk) error {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// Done writes the terminal OpenAI-compatible stream marker.
func (s *StreamWriter) Done() error {
	if s == nil {
		return nil
	}
	if _, err := fmt.Fprint(s.w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// streamHandler handles server-sent event streaming for chat completions.
