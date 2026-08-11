package retrieval

import (
	"context"
	"errors"
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

// fakeBackend is a minimal Backend used to prove Store correctly delegates
// to whatever backend it's given, independent of memoryBackend's own logic.
type fakeBackend struct {
	getErr     error
	getOK      bool
	getContent string
	putCalls   []struct{ id, content string }
}

func (f *fakeBackend) Put(_ context.Context, id, content string, _ time.Duration) error {
	f.putCalls = append(f.putCalls, struct{ id, content string }{id, content})
	return nil
}

func (f *fakeBackend) Get(_ context.Context, _ string) (string, bool, error) {
	return f.getContent, f.getOK, f.getErr
}

func TestStore_WithBackend_PutDelegatesToBackend(t *testing.T) {
	fb := &fakeBackend{}
	s := NewStoreWithBackend(fb)
	id := s.Put(context.Background(), "hello", time.Minute)

	if len(fb.putCalls) != 1 {
		t.Fatalf("expected exactly 1 Put on the backend, got %d", len(fb.putCalls))
	}
	if fb.putCalls[0].id != id || fb.putCalls[0].content != "hello" {
		t.Errorf("backend received (%q, %q), want (%q, %q)", fb.putCalls[0].id, fb.putCalls[0].content, id, "hello")
	}
}

func TestStore_WithBackend_GetErrorDegradesToMiss(t *testing.T) {
	fb := &fakeBackend{getErr: errors.New("backend unreachable"), getOK: true, getContent: "should never surface"}
	s := NewStoreWithBackend(fb)

	got, ok := s.Get(context.Background(), "ret_whatever")
	if ok {
		t.Fatal("expected a miss when the backend errors, not a hit — must degrade, not hang or propagate the error")
	}
	if got != "" {
		t.Errorf("expected empty content on a degraded miss, got %q", got)
	}
}

func TestStore_TwoInstancesSharingABackend_DistinctIDsNeverCollide(t *testing.T) {
	// Simulates two ixr replicas sharing one backend (the point of
	// NewStoreWithBackend): each mints its own IDs independently, and they
	// must never collide, or one replica's Get could resolve to another
	// replica's content.
	shared := newMemoryBackend(0)
	replicaA := NewStoreWithBackend(shared)
	replicaB := NewStoreWithBackend(shared)

	var idsA, idsB []string
	for i := 0; i < 200; i++ {
		idsA = append(idsA, replicaA.Put(context.Background(), "from-a", time.Minute))
		idsB = append(idsB, replicaB.Put(context.Background(), "from-b", time.Minute))
	}

	seen := make(map[string]bool, 400)
	for _, id := range append(idsA, idsB...) {
		if seen[id] {
			t.Fatalf("ID collision across replicas: %q", id)
		}
		seen[id] = true
	}

	// And a replica can resolve an ID minted by the other, proving the
	// shared backend actually shares state (not just avoids collisions).
	got, ok := replicaB.Get(context.Background(), idsA[0])
	if !ok || got != "from-a" {
		t.Errorf("replicaB.Get(idsA[0]) = (%q, %v), want (%q, true)", got, ok, "from-a")
	}
}
