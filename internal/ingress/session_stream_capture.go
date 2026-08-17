package ingress

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// sseCaptureWriter tees every byte written to the real client while also
// buffering it, so SessionMiddleware can reconstruct the assistant's full
// reply after the stream finishes without adding any latency to what the
// client sees — bytes reach the client the instant they're written, exactly
// as before; the buffering only feeds a parse pass that happens after the
// response is already fully delivered.
//
// Must implement http.Flusher: ChatHandler.handleStream type-asserts the
// ResponseWriter it's given to *http.Flusher before it will stream at all
// (`flusher, ok := w.(http.Flusher)`), so a wrapper that doesn't forward
// this would break streaming entirely, not just silently skip capture.
type sseCaptureWriter struct {
	http.ResponseWriter
	buf bytes.Buffer
}

func (w *sseCaptureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	// Only buffer what was actually written to the client, not the full
	// input, so a short write can't make the captured transcript claim
	// more than the client actually received.
	w.buf.Write(p[:n])
	return n, err
}

func (w *sseCaptureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// parseSSEAssistantTurn reconstructs the assistant's full reply from a
// captured SSE byte stream in ixr's own outbound chunk format
// (sseChunkEnvelope/sseDelta, stream.go) — the same package, so this is a
// decode of exactly what writeSSEChunk encoded, not a reimplementation of
// someone else's wire format.
//
// Returns ok=false when no chunk carried any content or tool calls (an
// error response sent before streaming began — writeError's plain JSON
// body doesn't parse as any "data: " line at all — or a stream that opened
// and then failed before producing anything) — nothing worth appending to
// session history either way.
func parseSSEAssistantTurn(data []byte) (schema.Message, bool) {
	msg := schema.Message{Role: "assistant"}
	var content strings.Builder
	found := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// SSE payloads can exceed bufio.Scanner's 64KB default token size on a
	// single long chunk; match the generosity providers' own SSE readers
	// use elsewhere in this codebase rather than silently truncating.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var env sseChunkEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue // malformed chunk — skip, matching provider-side SSE parsers' own tolerance
		}
		if len(env.Choices) == 0 {
			continue
		}
		delta := env.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			found = true
		}
		if len(delta.ToolCalls) > 0 {
			// ixr's outbound chunks carry whole tool_calls per chunk, not
			// fragments reassembled character-by-character the way some
			// providers' own wire formats do — every provider adapter's
			// Stream() already reassembles fragments internally before
			// handing a chunk to ChatHandler's onChunk (see e.g.
			// openaicompat's toolCallAccumulator). The last non-empty set
			// seen is the complete one.
			msg.ToolCalls = delta.ToolCalls
			found = true
		}
	}

	msg.Content = content.String()
	return msg, found
}
