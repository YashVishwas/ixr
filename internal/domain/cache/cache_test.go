package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeReq(model, content string) *schema.RequestEnvelope {
	return &schema.RequestEnvelope{
		Model:    model,
		Messages: []schema.Message{{Role: "user", Content: content}},
	}
}

func makeResp(id string) *schema.ResponseEnvelope {
	return &schema.ResponseEnvelope{ID: id}
}

func TestKey_Deterministic(t *testing.T) {
	req := makeReq("gpt-4o", "hello")
	k1 := Key(req)
	k2 := Key(req)
	if k1 != k2 {
		t.Fatal("Key is not deterministic")
	}
	if len(k1) != 64 {
		t.Fatalf("expected 64-char hex, got %d", len(k1))
	}
}

func TestKey_DifferentModels(t *testing.T) {
	k1 := Key(makeReq("gpt-4o", "hi"))
	k2 := Key(makeReq("claude-3-5-sonnet", "hi"))
	if k1 == k2 {
		t.Fatal("different models produced the same key")
	}
}

func TestKey_DifferentContent(t *testing.T) {
	k1 := Key(makeReq("gpt-4o", "hello"))
	k2 := Key(makeReq("gpt-4o", "world"))
	if k1 == k2 {
		t.Fatal("different content produced the same key")
	}
}

func TestMemory_HitAndMiss(t *testing.T) {
	c := NewMemory(100, time.Minute)
	req := makeReq("gpt-4o", "hello")
	key := Key(req)

	// Miss on empty cache.
	if _, ok := c.Get(context.Background(), key); ok {
		t.Fatal("expected miss, got hit on empty cache")
	}

	// Store, then hit.
	resp := makeResp("r1")
	c.Set(context.Background(), key, resp, 0)
	got, ok := c.Get(context.Background(), key)
	if !ok {
		t.Fatal("expected hit after Set")
	}
	if got.ID != "r1" {
		t.Fatalf("got ID=%q, want r1", got.ID)
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	c := NewMemory(100, time.Millisecond)
	key := Key(makeReq("m", "q"))
	c.Set(context.Background(), key, makeResp("r"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get(context.Background(), key); ok {
		t.Fatal("expected TTL-expired entry to miss")
	}
}

func TestMemory_Invalidate(t *testing.T) {
	c := NewMemory(100, time.Minute)
	key := Key(makeReq("m", "q"))
	c.Set(context.Background(), key, makeResp("r"), 0)
	c.Invalidate(context.Background(), key)
	if _, ok := c.Get(context.Background(), key); ok {
		t.Fatal("expected miss after Invalidate")
	}
}

func TestMemory_Eviction(t *testing.T) {
	c := NewMemory(3, time.Minute)
	for i := range 5 {
		key := Key(makeReq("m", string(rune('a'+i))))
		c.Set(context.Background(), key, makeResp("r"), 0)
	}
	if c.Size() > 3 {
		t.Fatalf("cache exceeded maxSize: size=%d", c.Size())
	}
}

func TestMemory_ConcurrentSafe(t *testing.T) {
	c := NewMemory(64, time.Minute)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := Key(makeReq("m", string(rune('a'+i%26))))
			c.Set(context.Background(), key, makeResp("r"), 0)
			c.Get(context.Background(), key)
		}(i)
	}
	wg.Wait()
}
