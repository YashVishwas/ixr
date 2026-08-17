package cbstate

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
)

func newTestRedis(t *testing.T, ttl time.Duration) *Redis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client, ttl)
}

var _ circuitbreaker.StateStore = (*Redis)(nil)

func TestRedis_SaveThenLoad_RoundTrips(t *testing.T) {
	r := newTestRedis(t, time.Minute)
	ctx := context.Background()

	if err := r.Save(ctx, "gpt-4o", circuitbreaker.StateOpen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := r.Load(ctx, "gpt-4o")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != circuitbreaker.StateOpen {
		t.Errorf("got %v, want StateOpen", got)
	}
}

func TestRedis_Load_UnknownModel_Errors(t *testing.T) {
	r := newTestRedis(t, time.Minute)
	_, err := r.Load(context.Background(), "never-saved")
	if err == nil {
		t.Fatal("expected an error for a model with no saved state")
	}
}

func TestRedis_AllThreeStates_RoundTrip(t *testing.T) {
	r := newTestRedis(t, time.Minute)
	ctx := context.Background()

	for _, s := range []circuitbreaker.State{circuitbreaker.StateClosed, circuitbreaker.StateOpen, circuitbreaker.StateHalfOpen} {
		if err := r.Save(ctx, "m", s); err != nil {
			t.Fatalf("Save(%v): %v", s, err)
		}
		got, err := r.Load(ctx, "m")
		if err != nil {
			t.Fatalf("Load after Save(%v): %v", s, err)
		}
		if got != s {
			t.Errorf("got %v, want %v", got, s)
		}
	}
}

func TestRedis_ZeroTTL_NeverExpires(t *testing.T) {
	r := newTestRedis(t, 0)
	ctx := context.Background()
	if err := r.Save(ctx, "gpt-4o", circuitbreaker.StateOpen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := r.Load(ctx, "gpt-4o"); err != nil {
		t.Fatalf("expected a hit with ttl<=0 (no expiry), got: %v", err)
	}
}

func TestRedis_KeysAreNamespaced(t *testing.T) {
	r := newTestRedis(t, time.Minute)
	ctx := context.Background()
	_ = r.Save(ctx, "gpt-4o", circuitbreaker.StateOpen)

	raw, err := r.client.Get(ctx, "cbstate:gpt-4o").Result()
	if err != nil {
		t.Fatalf("expected the raw key to be namespaced with cbstate:, lookup failed: %v", err)
	}
	if raw != "1" { // StateOpen == 1
		t.Errorf("got %q, want %q", raw, "1")
	}
}

func TestRedis_UsedThroughRegistry_SharedAcrossTwoInstanceHandles(t *testing.T) {
	// Proves the whole point: two independent Redis client handles
	// (standing in for two ixr replicas) pointed at the same server share
	// circuit breaker state through the Registry's normal API.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientA.Close()
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientB.Close()

	policy := circuitbreaker.Policy{
		SuccessRateThreshold: 0.90,
		WindowDuration:       10 * time.Second,
		MinRequests:          3,
		HalfOpenAfter:        time.Minute,
		ProbeCount:           2,
	}
	replicaA := circuitbreaker.NewRegistry(policy).WithStateStore(New(clientA, time.Minute))
	replicaB := circuitbreaker.NewRegistry(policy).WithStateStore(New(clientB, time.Minute))

	// Replica A trips the breaker for "gpt-4o".
	for i := 0; i < 3; i++ {
		replicaA.RecordOutcome("gpt-4o", false)
	}

	// Give the async Save (see registry.go's RecordOutcome) a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := New(clientB, time.Minute).Load(context.Background(), "gpt-4o"); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Replica B has never seen "gpt-4o" fail locally, but its first
	// IsAllowed call should seed from the shared state A just wrote.
	if replicaB.IsAllowed("gpt-4o") {
		t.Fatal("expected replica B to see gpt-4o as tripped via the shared Redis state, not start fresh/Closed")
	}
}
