package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/retrieval"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// TestChatHandler_ResolvesRetrieveCall_CallerNeverSeesIt is the load-bearing
// integration test for reversible compression's response side: a provider
// that calls the synthetic retrieval tool on its first turn, then answers
// for real once given the expanded content on the second — proving
// resolveRetrieval actually replays the extra hop and that the HTTP caller
// only ever sees the final answer, never the intermediate tool call.
func TestChatHandler_ResolvesRetrieveCall_CallerNeverSeesIt(t *testing.T) {
	store := retrieval.NewStore(0)
	id := store.Put(context.Background(), "the full original content", time.Minute)

	var calls int32
	var secondHopMessages []schema.Message
	p := &stubProvider{name: "test", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &schema.ResponseEnvelope{
				ID: "hop1",
				Choices: []schema.Choice{{
					Message: schema.Message{
						Role: "assistant",
						ToolCalls: []schema.ToolCall{{
							ID:       "call_1",
							Type:     "function",
							Function: schema.ToolFunction{Name: retrieval.ToolName, Arguments: `{"id":"` + id + `"}`},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: schema.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
			}, nil
		}
		secondHopMessages = req.Messages
		return &schema.ResponseEnvelope{
			ID:      "hop2-final",
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "the real answer"}, FinishReason: "stop"}},
			Usage:   schema.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220},
		}, nil
	}}

	h := NewChatHandler(fixedRouter(p), nil, WithRetrieval(store))

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"what does the full content say?"}]}`
	w := post(h, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 provider calls (original + one retrieval hop), got %d", got)
	}

	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "hop2-final" {
		t.Errorf("expected the caller to receive the final (second-hop) response, got ID=%q", resp.ID)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "the real answer" {
		t.Fatalf("expected the final answer, got %+v", resp.Choices)
	}
	// The intermediate tool call must not leak into what the caller sees.
	if len(resp.Choices[0].Message.ToolCalls) != 0 {
		t.Errorf("expected no tool calls in the response the caller receives, got %+v", resp.Choices[0].Message.ToolCalls)
	}

	// Usage should reflect both hops, not just the second.
	if resp.Usage.PromptTokens != 300 || resp.Usage.CompletionTokens != 30 || resp.Usage.TotalTokens != 330 {
		t.Errorf("expected usage summed across both hops (300/30/330), got %+v", resp.Usage)
	}

	// The second hop must actually have received the retrieved content, not
	// just an empty or placeholder tool result.
	foundRetrieved := false
	for _, m := range secondHopMessages {
		if m.Role == "tool" && strings.Contains(m.Content, "the full original content") {
			foundRetrieved = true
		}
	}
	if !foundRetrieved {
		t.Errorf("expected the second hop's messages to include the retrieved content, got %+v", secondHopMessages)
	}
}

// TestChatHandler_RetrievalMiss_DoesNotBlockTheRequest confirms a store
// miss (expired/evicted entry) degrades gracefully — the second hop still
// happens, with a placeholder explaining the content is gone, rather than
// erroring out the whole request.
func TestChatHandler_RetrievalMiss_DoesNotBlockTheRequest(t *testing.T) {
	store := retrieval.NewStore(0) // empty — any id is a miss

	var calls int32
	p := &stubProvider{name: "test", chat: func(_ context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &schema.ResponseEnvelope{
				Choices: []schema.Choice{{
					Message: schema.Message{
						Role: "assistant",
						ToolCalls: []schema.ToolCall{{
							ID:       "call_1",
							Type:     "function",
							Function: schema.ToolFunction{Name: retrieval.ToolName, Arguments: `{"id":"ret_gone"}`},
						}},
					},
				}},
			}, nil
		}
		return &schema.ResponseEnvelope{
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "answered anyway"}}},
		}, nil
	}}

	h := NewChatHandler(fixedRouter(p), nil, WithRetrieval(store))
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Choices[0].Message.Content != "answered anyway" {
		t.Errorf("expected the request to still complete on a retrieval miss, got %+v", resp.Choices)
	}
}

// TestChatHandler_SecondRetrieveCall_PassedThroughUnresolved confirms the
// one-extra-hop bound: if the model asks to retrieve again even after
// getting the expanded content, that second call is returned to the caller
// as-is rather than triggering a third hop.
func TestChatHandler_SecondRetrieveCall_PassedThroughUnresolved(t *testing.T) {
	store := retrieval.NewStore(0)
	id := store.Put(context.Background(), "original", time.Minute)

	var calls int32
	retrieveCall := schema.ToolCall{
		ID:       "call_1",
		Type:     "function",
		Function: schema.ToolFunction{Name: retrieval.ToolName, Arguments: `{"id":"` + id + `"}`},
	}
	p := &stubProvider{name: "test", chat: func(_ context.Context, _ *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		atomic.AddInt32(&calls, 1)
		// Always asks to retrieve, regardless of hop — simulates a model
		// that won't stop calling the tool.
		return &schema.ResponseEnvelope{
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", ToolCalls: []schema.ToolCall{retrieveCall}}}},
		}, nil
	}}

	h := NewChatHandler(fixedRouter(p), nil, WithRetrieval(store))
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 provider calls (bounded to one retrieval hop), got %d", got)
	}
	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) == 0 {
		t.Errorf("expected the second hop's unresolved retrieve call to be passed through to the caller, got %+v", resp.Choices[0].Message)
	}
}

// TestChatHandler_NoRetrievalStoreConfigured_PassesToolCallsThroughUnchanged
// guards backward compatibility: without WithRetrieval, tool calls
// (including one that happens to be named ixr_retrieve, however unlikely)
// must pass straight through exactly as before this feature existed.
func TestChatHandler_NoRetrievalStoreConfigured_PassesToolCallsThroughUnchanged(t *testing.T) {
	p := &stubProvider{name: "test", resp: &schema.ResponseEnvelope{
		Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", ToolCalls: []schema.ToolCall{
			{ID: "c1", Type: "function", Function: schema.ToolFunction{Name: "get_weather"}},
		}}}},
	}}
	h := NewChatHandler(fixedRouter(p), nil) // no WithRetrieval
	w := post(h, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	var resp schema.ResponseEnvelope
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("expected the tool call to pass through unchanged, got %+v", resp.Choices[0].Message)
	}
}
