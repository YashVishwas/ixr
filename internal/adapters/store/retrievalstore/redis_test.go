package retrievalstore

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/YashVishwas/ixr/internal/domain/retrieval"
)

func newTestRedis(t *testing.T) *Redis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client)
}

// var _ retrieval.Backend ensures Redis actually satisfies the interface
// internal/domain/retrieval.Store depends on — a compile-time check, not a
// runtime one, but worth keeping explicit since retrievalstore imports
// retrieval only for this assertion.
var _ retrieval.Backend = (*Redis)(nil)

func TestRedis_PutThenGet_RoundTrips(t *testing.T) {
	r := newTestRedis(t)
	ctx := context.Background()

	if err := r.Put(ctx, "id1", "the original content", time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := r.Get(ctx, "id1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected a hit")
	}
	if got != "the original content" {
		t.Errorf("got %q, want %q", got, "the original content")
	}
}

func TestRedis_Get_UnknownID_Miss(t *testing.T) {
	r := newTestRedis(t)
	_, ok, err := r.Get(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected a miss for an unknown ID")
	}
}

func TestRedis_ZeroTTL_NeverExpires(t *testing.T) {
	r := newTestRedis(t)
	ctx := context.Background()
	if err := r.Put(ctx, "id1", "persists", 0); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, ok, err := r.Get(ctx, "id1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected a hit — ttl<=0 means no expiry")
	}
}

func TestRedis_NegativeTTL_TreatedAsNoExpiry(t *testing.T) {
	r := newTestRedis(t)
	ctx := context.Background()
	if err := r.Put(ctx, "id1", "persists", -time.Second); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, ok, err := r.Get(ctx, "id1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected a hit — negative ttl should be treated like ttl<=0 (no expiry), not rejected")
	}
}

func TestRedis_KeysAreNamespaced(t *testing.T) {
	r := newTestRedis(t)
	ctx := context.Background()
	_ = r.Put(ctx, "id1", "content", time.Minute)

	// Reach past the abstraction to confirm the raw Redis key carries the
	// documented "retrieval:" prefix, so this backend can share a Redis
	// instance/database with other ixr state without key collisions.
	raw, err := r.client.Get(ctx, "retrieval:id1").Result()
	if err != nil {
		t.Fatalf("expected the raw key to be namespaced with retrieval:, lookup failed: %v", err)
	}
	if raw != "content" {
		t.Errorf("got %q, want %q", raw, "content")
	}
}

func TestRedis_UsedThroughStore_SharedAcrossTwoBackendHandles(t *testing.T) {
	// Proves the whole point of this backend: two independent Redis client
	// handles (standing in for two ixr replicas) pointed at the same
	// server resolve each other's IDs, unlike the in-memory backend.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientA.Close()
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientB.Close()

	replicaA := retrieval.NewStoreWithBackend(New(clientA))
	replicaB := retrieval.NewStoreWithBackend(New(clientB))

	id := replicaA.Put(context.Background(), "written by replica A", time.Minute)
	got, ok := replicaB.Get(context.Background(), id)
	if !ok {
		t.Fatal("expected replica B to resolve an ID minted by replica A via the shared Redis backend")
	}
	if got != "written by replica A" {
		t.Errorf("got %q, want %q", got, "written by replica A")
	}
}
