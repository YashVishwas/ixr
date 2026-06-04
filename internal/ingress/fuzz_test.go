package ingress

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// FuzzChatHandlerBody ensures the chat handler never panics on arbitrary JSON input.
func FuzzChatHandlerBody(f *testing.F) {
	f.Add([]byte(`{"model":"gpt-4o","messages":[]}`))
	f.Add([]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))

	p := &stubProvider{name: "test", resp: &schema.ResponseEnvelope{ID: "r1", Choices: []schema.Choice{{}}}}
	h := NewChatHandler(fixedRouter(p), nil)

	f.Fuzz(func(t *testing.T, body []byte) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		// Must not panic.
		h.ServeHTTP(w, req)
	})
}

// FuzzCacheKey ensures cache.Key never panics on arbitrary request shapes.
func FuzzCacheKey(f *testing.F) {
	f.Add("gpt-4o", "user", "hello")
	f.Add("", "", "")
	f.Add("a", "b", string([]byte{0x00, 0xFF, 0xFE}))

	f.Fuzz(func(t *testing.T, model, role, content string) {
		req := &schema.RequestEnvelope{
			Model:    model,
			Messages: []schema.Message{{Role: role, Content: content}},
		}
		// Must not panic.
		_ = cacheKeyForFuzz(req)
	})
}

// stubStreamProvider satisfies provider.Provider for fuzz tests.
type stubStreamProvider struct{ stubProvider }

func (s *stubStreamProvider) Stream(_ context.Context, _ *schema.RequestEnvelope, _ func(provider.StreamChunk) error) error {
	return s.err
}

func cacheKeyForFuzz(req *schema.RequestEnvelope) string {
	return cache.Key(req)
}
