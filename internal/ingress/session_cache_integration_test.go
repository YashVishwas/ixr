package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/internal/domain/session"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// TestSessionPlusSemanticCache_SecondQuestionNotServedFirstAnswer is the
// end-to-end version of the RFC's Open Question #10 regression: with real
// SessionMiddleware -> CacheMiddleware -> ChatHandler wiring (previously
// untested — no test exercised this composed chain at all), asking two
// unrelated questions back-to-back in the same session must not return the
// first question's cached answer for the second question, even though
// SessionMiddleware injects the first turn as history before the cache
// ever sees the second request.
func TestSessionPlusSemanticCache_SecondQuestionNotServedFirstAnswer(t *testing.T) {
	calls := 0
	p := &stubProvider{name: "test", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		calls++
		last := strings.ToLower(req.Messages[len(req.Messages)-1].Content)
		if strings.Contains(last, "revolution") || strings.Contains(last, "figures") || strings.Contains(last, "consequences") {
			return &schema.ResponseEnvelope{ID: "r1", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "The revolution began in 1789 for several interlocking reasons."}}}}, nil
		}
		return &schema.ResponseEnvelope{ID: "r2", Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "Mix flour, sugar, and cocoa..."}}}}, nil
	}}
	chatHandler := NewChatHandler(fixedRouter(p), nil)

	mem := cache.NewMemory(100, time.Minute)
	exact := &cache.ExactCache{Memory: mem}
	backend := cache.NewMemorySemanticBackend(100)
	semanticCache := cache.NewSemanticCache(exact, backend, cache.WordVectorizer{}, 0.92)
	cacheLayer := NewCacheMiddleware(semanticCache, time.Minute, chatHandler)

	store := session.NewMemorySessionStore(time.Hour, 50)
	handler := NewSessionMiddleware(store, cacheLayer)

	sessionID := "test-session"
	post := func(content string) *schema.ResponseEnvelope {
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + content + `"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerSessionID, sessionID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var resp schema.ResponseEnvelope
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
		}
		return &resp
	}

	// Build up several turns of history on one topic first — a single
	// prior turn isn't enough shared text to dominate the token-overlap
	// score; this matches the multi-turn depth a real session accrues.
	first := post("Can you give me an overview of the French Revolution, its main causes, and why it started when it did")
	if !strings.Contains(first.Choices[0].Message.Content, "1789") {
		t.Fatalf("first answer: got %q, want the revolution answer", first.Choices[0].Message.Content)
	}
	post("Who were the key figures involved in the French Revolution and what roles did they play")
	post("What were the long term consequences of the French Revolution for France and for Europe")

	// A completely unrelated new question in the same session — three
	// turns of French Revolution history are now injected ahead of it.
	fourth := post("Give me a recipe for chocolate cake")
	if strings.Contains(fourth.Choices[0].Message.Content, "1789") {
		t.Fatalf("unrelated question incorrectly reused an earlier cached answer: %q", fourth.Choices[0].Message.Content)
	}
	if !strings.Contains(fourth.Choices[0].Message.Content, "flour") {
		t.Errorf("fourth answer: got %q, want the cake answer", fourth.Choices[0].Message.Content)
	}
	if calls != 4 {
		t.Errorf("expected 4 real provider calls (no false cache hit along the way), got %d", calls)
	}
}
