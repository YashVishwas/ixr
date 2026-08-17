package bedrock

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/YashVishwas/ixr/pkg/provider"
)

// bedrockChunk builds one AWS event-stream "chunk" frame wrapping innerJSON
// the way Bedrock actually does: {"bytes": base64(innerJSON)}.
func bedrockChunk(t *testing.T, innerJSON string) []byte {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(innerJSON))
	payload := []byte(`{"bytes":"` + encoded + `"}`)
	return encodeEventStreamMessage(t, map[string]string{":event-type": "chunk", ":content-type": "application/json"}, payload)
}

// TestStreamBedrockEvents_FullSequence is the end-to-end regression test
// for real Bedrock streaming: a synthetic message_start →
// content_block_delta (x2) → message_delta → message_stop sequence,
// asserting the caller receives the right text deltas, in order, plus a
// final chunk carrying the finish reason and usage — and that the stream
// terminates cleanly at message_stop rather than reading past it.
func TestStreamBedrockEvents_FullSequence(t *testing.T) {
	var frames []byte
	frames = append(frames, bedrockChunk(t, `{"type":"message_start","message":{"id":"msg_123","model":"anthropic.claude-3-5-sonnet-20241022-v2:0","usage":{"input_tokens":10}}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"content_block_delta","delta":{"type":"text_delta","text":", world"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"message_stop"}`)...)

	var chunks []provider.StreamChunk
	err := streamBedrockEvents(bytes.NewReader(frames), "anthropic.claude-3-5-sonnet-20241022-v2:0", func(c provider.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) != 3 {
		t.Fatalf("chunks: got %d, want 3 (2 text deltas + 1 finish), got %+v", len(chunks), chunks)
	}
	if chunks[0].ID != "msg_123" || chunks[0].Delta.Content != "Hello" {
		t.Errorf("chunk 0: got %+v", chunks[0])
	}
	if chunks[1].Delta.Content != ", world" {
		t.Errorf("chunk 1: got %+v", chunks[1])
	}
	final := chunks[2]
	if final.FinishReason != "stop" {
		t.Errorf("finish reason: got %q, want %q", final.FinishReason, "stop")
	}
	if final.Usage == nil || final.Usage.PromptTokens != 10 || final.Usage.CompletionTokens != 5 || final.Usage.TotalTokens != 15 {
		t.Errorf("usage: got %+v", final.Usage)
	}
}

// TestStreamBedrockEvents_SkipsToolCallDeltas confirms tool-call streaming
// (input_json_delta) is silently skipped rather than mis-decoded as text —
// matching the documented scope note that tool calls aren't supported in
// this adapter's streaming path (or its non-streaming path either).
func TestStreamBedrockEvents_SkipsToolCallDeltas(t *testing.T) {
	var frames []byte
	frames = append(frames, bedrockChunk(t, `{"type":"message_start","message":{"id":"msg_1"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"content_block_delta","delta":{"type":"input_json_delta","text":"{\"a\":"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"message_stop"}`)...)

	var chunks []provider.StreamChunk
	err := streamBedrockEvents(bytes.NewReader(frames), "some-model", func(c provider.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected the input_json_delta to be skipped, got %+v", chunks)
	}
}

// TestStreamBedrockEvents_ExceptionEventReturnsError confirms a Bedrock
// stream-level exception (":exception-type" header set) surfaces as a Go
// error rather than being silently ignored or misread as a normal chunk.
func TestStreamBedrockEvents_ExceptionEventReturnsError(t *testing.T) {
	frame := encodeEventStreamMessage(t,
		map[string]string{":exception-type": "modelStreamErrorException", ":content-type": "application/json"},
		[]byte(`{"message":"model overloaded"}`),
	)

	err := streamBedrockEvents(bytes.NewReader(frame), "some-model", func(provider.StreamChunk) error {
		t.Fatal("fn should not be called for an exception event")
		return nil
	})
	if err == nil {
		t.Fatal("expected an error for an exception event")
	}
}

// TestStreamBedrockEvents_CleanEmptyStream confirms a stream with no
// frames at all (e.g. a zero-length body) returns nil, not an error.
func TestStreamBedrockEvents_CleanEmptyStream(t *testing.T) {
	err := streamBedrockEvents(bytes.NewReader(nil), "some-model", func(provider.StreamChunk) error {
		t.Fatal("fn should not be called for an empty stream")
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil for a clean empty stream, got %v", err)
	}
}

// TestStreamBedrockEvents_FnErrorPropagates confirms an error returned by
// the caller's callback aborts the stream and is returned, rather than
// being swallowed and the stream continuing.
func TestStreamBedrockEvents_FnErrorPropagates(t *testing.T) {
	var frames []byte
	frames = append(frames, bedrockChunk(t, `{"type":"message_start","message":{"id":"msg_1"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`)...)
	frames = append(frames, bedrockChunk(t, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"should not reach this"}}`)...)

	callCount := 0
	wantErr := errStop
	err := streamBedrockEvents(bytes.NewReader(frames), "some-model", func(provider.StreamChunk) error {
		callCount++
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("expected the callback's error to propagate, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected the stream to stop after the first callback error, got %d calls", callCount)
	}
}

var errStop = errors.New("stop")
