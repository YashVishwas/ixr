package ingress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/session"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// --- parseSSEAssistantTurn unit tests ---

func TestParseSSEAssistantTurn_ConcatenatesContentAcrossChunks(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	msg, ok := parseSSEAssistantTurn([]byte(stream))
	if !ok {
		t.Fatal("expected ok=true for a stream that produced content")
	}
	if msg.Content != "Hello world" {
		t.Errorf("content: got %q, want %q", msg.Content, "Hello world")
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want assistant", msg.Role)
	}
}

func TestParseSSEAssistantTurn_ToolCallsCaptured(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Austin\"}"}}]}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	msg, ok := parseSSEAssistantTurn([]byte(stream))
	if !ok {
		t.Fatal("expected ok=true for a stream that produced tool calls")
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_calls: got %+v", msg.ToolCalls)
	}
}

func TestParseSSEAssistantTurn_EmptyStream_NotOK(t *testing.T) {
	_, ok := parseSSEAssistantTurn([]byte(""))
	if ok {
		t.Fatal("expected ok=false for an empty stream")
	}
}

func TestParseSSEAssistantTurn_PlainJSONErrorBody_NotOK(t *testing.T) {
	// writeError's body shape — no "data: " lines at all, since the error
	// is written before streaming begins.
	body := `{"error":{"type":"circuit_open","message":"model temporarily unavailable"}}`
	_, ok := parseSSEAssistantTurn([]byte(body))
	if ok {
		t.Fatal("expected ok=false for a non-SSE error body")
	}
}

func TestParseSSEAssistantTurn_MalformedLineSkipped(t *testing.T) {
	stream := "data: not valid json\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"still works\"}}]}\n\n"

	msg, ok := parseSSEAssistantTurn([]byte(stream))
	if !ok {
		t.Fatal("expected ok=true — the malformed line should be skipped, not abort parsing")
	}
	if msg.Content != "still works" {
		t.Errorf("content: got %q, want %q", msg.Content, "still works")
	}
}

func TestParseSSEAssistantTurn_DoneOnlyStream_NotOK(t *testing.T) {
	_, ok := parseSSEAssistantTurn([]byte("data: [DONE]\n\n"))
	if ok {
		t.Fatal("expected ok=false when the only line is the terminal [DONE] marker")
	}
}

// --- sseCaptureWriter behavior ---

func TestSSECaptureWriter_ForwardsBytesToRealClient(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &sseCaptureWriter{ResponseWriter: rec}

	_, _ = w.Write([]byte("data: hello\n\n"))

	if rec.Body.String() != "data: hello\n\n" {
		t.Errorf("client did not receive the written bytes: got %q", rec.Body.String())
	}
	if w.buf.String() != "data: hello\n\n" {
		t.Errorf("capture buffer mismatch: got %q", w.buf.String())
	}
}

func TestSSECaptureWriter_ImplementsFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &sseCaptureWriter{ResponseWriter: rec}
	if _, ok := any(w).(http.Flusher); !ok {
		t.Fatal("sseCaptureWriter must implement http.Flusher or ChatHandler.handleStream's type assertion fails and streaming breaks entirely")
	}
	w.Flush()
	if !rec.Flushed {
		t.Error("expected Flush() to be forwarded to the underlying ResponseWriter")
	}
}

// --- End-to-end: SessionMiddleware -> ChatHandler, streaming ---

func TestSessionMiddleware_StreamingResponse_CapturedIntoSessionHistory(t *testing.T) {
	p := &stubProvider{name: "test", stream: func(_ context.Context, _ *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
		if err := fn(provider.StreamChunk{Delta: schema.Message{Role: "assistant", Content: "The "}}); err != nil {
			return err
		}
		if err := fn(provider.StreamChunk{Delta: schema.Message{Content: "answer is 42."}, FinishReason: "stop"}); err != nil {
			return err
		}
		return nil
	}}
	chatHandler := NewChatHandler(fixedRouter(p), nil)
	store := session.NewMemorySessionStore(time.Hour, 50)
	handler := NewSessionMiddleware(store, chatHandler)

	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"what is the answer?"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerSessionID, "s1")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// The client must still receive the real SSE stream, unaffected by capture.
	if !strings.Contains(w.Body.String(), "The ") || !strings.Contains(w.Body.String(), "answer is 42.") {
		t.Fatalf("client did not receive the streamed content: %s", w.Body.String())
	}

	// identity.FromContext with no auth context resolves to TenantID
	// "default" — SessionMiddleware's store key is "<tenantID>:<sessionID>".
	history, ok := store.Get(context.Background(), "default:s1")
	if !ok {
		t.Fatal("expected the turn to have been appended to session history")
	}
	if len(history) != 2 {
		t.Fatalf("history: got %d messages, want 2 (user + assistant)", len(history))
	}
	if history[0].Content != "what is the answer?" || history[0].Role != "user" {
		t.Errorf("user turn: got %+v", history[0])
	}
	if history[1].Content != "The answer is 42." || history[1].Role != "assistant" {
		t.Errorf("assistant turn: got %+v", history[1])
	}
}

func TestSessionMiddleware_StreamingResponse_ErrorBeforeStreamBegins_NoAppend(t *testing.T) {
	// The provider errors before producing any chunk — mirrors what an
	// immediate connection failure or auth rejection from upstream looks
	// like: no content ever reaches onChunk, so nothing should be appended.
	p := &stubProvider{name: "test", stream: func(_ context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
		return errors.New("upstream error")
	}}
	chatHandler := NewChatHandler(fixedRouter(p), nil)
	store := session.NewMemorySessionStore(time.Hour, 50)
	handler := NewSessionMiddleware(store, chatHandler)

	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerSessionID, "s2")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if _, ok := store.Get(context.Background(), "default:s2"); ok {
		t.Fatal("expected no session entry when the stream produced no content at all")
	}
}
