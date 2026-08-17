package retrieval

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStore_PutThenGet_RoundTrips(t *testing.T) {
	s := NewStore(0)
	ctx := context.Background()
	id := s.Put(ctx, "the original content", time.Minute)
	got, ok := s.Get(ctx, id)
	if !ok {
		t.Fatal("expected a hit")
	}
	if got != "the original content" {
		t.Errorf("got %q, want %q", got, "the original content")
	}
}

func TestStore_Get_UnknownID_Miss(t *testing.T) {
	s := NewStore(0)
	if _, ok := s.Get(context.Background(), "ret_does_not_exist"); ok {
		t.Fatal("expected a miss for an unknown ID")
	}
}

func TestStore_TTLExpiry(t *testing.T) {
	s := NewStore(0)
	ctx := context.Background()
	id := s.Put(ctx, "expires soon", time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.Get(ctx, id); ok {
		t.Fatal("expected a miss after TTL expiry")
	}
}

func TestStore_ZeroTTL_NeverExpires(t *testing.T) {
	s := NewStore(0)
	ctx := context.Background()
	id := s.Put(ctx, "persists", 0)
	time.Sleep(2 * time.Millisecond)
	if _, ok := s.Get(ctx, id); !ok {
		t.Fatal("expected a hit — ttl<=0 means no expiry")
	}
}

func TestStore_DistinctIDsPerPut(t *testing.T) {
	s := NewStore(0)
	ctx := context.Background()
	id1 := s.Put(ctx, "first", time.Minute)
	id2 := s.Put(ctx, "second", time.Minute)
	if id1 == id2 {
		t.Fatalf("expected distinct IDs, got %q twice", id1)
	}
	got1, _ := s.Get(ctx, id1)
	got2, _ := s.Get(ctx, id2)
	if got1 != "first" || got2 != "second" {
		t.Errorf("cross-contamination: id1=%q id2=%q", got1, got2)
	}
}

func TestStore_MaxSizeEvictsOldest(t *testing.T) {
	s := NewStore(2)
	ctx := context.Background()
	id1 := s.Put(ctx, "first", time.Minute)
	s.Put(ctx, "second", time.Minute)
	s.Put(ctx, "third", time.Minute) // should evict id1 (oldest)

	if s.Len() > 2 {
		t.Fatalf("store exceeded maxSize: len=%d", s.Len())
	}
	if _, ok := s.Get(ctx, id1); ok {
		t.Error("expected the oldest entry to have been evicted")
	}
}

func TestStore_ConcurrentPutGet_NoRace(t *testing.T) {
	s := NewStore(1000)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := s.Put(ctx, "content", time.Minute)
			s.Get(ctx, id)
		}()
	}
	wg.Wait()
}
