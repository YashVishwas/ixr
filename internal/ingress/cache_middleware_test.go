package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// TestCacheMiddleware_ConcurrentIdenticalMisses_Coalesce is the regression
// test for the gap this fix closes: before singleflight, a burst of
// concurrent identical requests each independently reached the provider on
// a cache miss, even though only one of them needed to.
func TestCacheMiddleware_ConcurrentIdenticalMisses_Coalesce(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	p := &stubProvider{name: "test", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		atomic.AddInt32(&calls, 1)
		<-release // hold every concurrent caller inside the same in-flight window
		return &schema.ResponseEnvelope{
			ID:      "shared-response",
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "the answer"}}},
		}, nil
	}}
	chatHandler := NewChatHandler(fixedRouter(p), nil)

	mem := cache.NewMemory(100, time.Minute)
	exact := &cache.ExactCache{Memory: mem}
	cacheLayer := NewCacheMiddleware(exact, time.Minute, chatHandler)

	const n = 10
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"what is the answer?"}]}`

	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = post(cacheLayer, body)
		}(i)
	}

	// Give every goroutine a chance to reach the provider stub (and block on
	// release) before letting any of them complete — otherwise an early
	// request could finish and populate the cache before the later ones
	// even start, which would prove caching works but not coalescing.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("provider called %d times for %d concurrent identical requests, want exactly 1", got, n)
	}

	for i, w := range results {
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, body = %s", i, w.Code, w.Body.String())
		}
		var resp schema.ResponseEnvelope
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("request %d: decode: %v", i, err)
		}
		if resp.ID != "shared-response" {
			t.Errorf("request %d: got response %q, want the shared response", i, resp.ID)
		}
	}
}

// TestCacheMiddleware_SequentialIdenticalRequests_SecondServedFromCache
// confirms this change didn't disturb the ordinary (non-concurrent) path:
// the second identical request after the first completes should be a real
// cache hit, not a second coalesced/upstream call.
func TestCacheMiddleware_SequentialIdenticalRequests_SecondServedFromCache(t *testing.T) {
	var calls int32
	p := &stubProvider{name: "test", chat: func(_ context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
		atomic.AddInt32(&calls, 1)
		return &schema.ResponseEnvelope{
			ID:      "r1",
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "the answer"}}},
		}, nil
	}}
	chatHandler := NewChatHandler(fixedRouter(p), nil)
	mem := cache.NewMemory(100, time.Minute)
	exact := &cache.ExactCache{Memory: mem}
	cacheLayer := NewCacheMiddleware(exact, time.Minute, chatHandler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"what is the answer?"}]}`

	w1 := post(cacheLayer, body)
	if got := w1.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("first request X-Cache = %q, want MISS", got)
	}

	w2 := post(cacheLayer, body)
	if got := w2.Header().Get("X-Cache"); got == "MISS" || got == "COALESCED" {
		t.Errorf("second (sequential, post-completion) request X-Cache = %q, want a real cache hit", got)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("provider called %d times across 2 sequential identical requests, want exactly 1", got)
	}
}
