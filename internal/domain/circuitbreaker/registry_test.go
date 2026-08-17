package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStateStore is an in-memory StateStore double for testing Registry's
// wiring, independent of any real backend.
type fakeStateStore struct {
	mu        sync.Mutex
	states    map[string]State
	loadErr   error
	saveErr   error
	saveCalls []struct {
		model string
		state State
	}
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{states: make(map[string]State)}
}

func (f *fakeStateStore) Load(_ context.Context, model string) (State, error) {
	if f.loadErr != nil {
		return StateClosed, f.loadErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.states[model]
	if !ok {
		return StateClosed, errors.New("not found")
	}
	return s, nil
}

func (f *fakeStateStore) Save(_ context.Context, model string, state State) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[model] = state
	f.saveCalls = append(f.saveCalls, struct {
		model string
		state State
	}{model, state})
	return nil
}

func (f *fakeStateStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saveCalls)
}

func TestRegistry_NoStateStore_BehavesExactlyAsBeforeTheFeature(t *testing.T) {
	r := NewRegistry(shortPolicy())
	if !r.IsAllowed("gpt-4o") {
		t.Fatal("expected a fresh model with no store to be allowed")
	}
}

func TestRegistry_WithStateStore_SeedsNewBreakerFromStore(t *testing.T) {
	store := newFakeStateStore()
	store.states["gpt-4o"] = StateOpen

	r := NewRegistry(shortPolicy()).WithStateStore(store)

	if r.IsAllowed("gpt-4o") {
		t.Fatal("expected a model another instance already tripped to seed as not-allowed on first use here")
	}
}

func TestRegistry_WithStateStore_UnknownModelSeedsClosedNotBlocked(t *testing.T) {
	store := newFakeStateStore() // no entries — every Load misses

	r := NewRegistry(shortPolicy()).WithStateStore(store)

	if !r.IsAllowed("brand-new-model") {
		t.Fatal("expected a model with no prior state anywhere to default to Closed (allowed)")
	}
}

func TestRegistry_WithStateStore_LoadErrorDegradesToLocalClosed(t *testing.T) {
	store := newFakeStateStore()
	store.loadErr = errors.New("redis unreachable")

	r := NewRegistry(shortPolicy()).WithStateStore(store)

	if !r.IsAllowed("gpt-4o") {
		t.Fatal("expected a Load error to degrade to local default state (Closed/allowed), not block the request")
	}
}

func TestRegistry_WithStateStore_TransitionPushesNewStateToStore(t *testing.T) {
	store := newFakeStateStore()
	r := NewRegistry(shortPolicy()).WithStateStore(store)

	for i := 0; i < 5; i++ {
		r.RecordOutcome("gpt-4o", false)
	}

	if r.get("gpt-4o").CurrentState() != StateOpen {
		t.Fatalf("expected the breaker itself to be Open after %d failures", 5)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.callCount() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	saved, err := store.Load(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("expected the transition to have been saved to the store: %v", err)
	}
	if saved != StateOpen {
		t.Errorf("saved state: got %v, want StateOpen", saved)
	}
}

func TestRegistry_WithStateStore_NonTransitioningOutcomesDoNotSave(t *testing.T) {
	store := newFakeStateStore()
	r := NewRegistry(shortPolicy()).WithStateStore(store)

	// A single success on an already-Closed breaker doesn't change state —
	// nothing should be pushed to the store for it.
	r.RecordOutcome("gpt-4o", true)

	time.Sleep(20 * time.Millisecond) // give any (incorrect) async save a chance to land
	if store.callCount() != 0 {
		t.Errorf("expected no store writes for a non-transitioning outcome, got %d", store.callCount())
	}
}

func TestRegistry_WithStateStore_SaveErrorDoesNotPanicOrBlock(t *testing.T) {
	store := newFakeStateStore()
	store.saveErr = errors.New("redis write failed")
	r := NewRegistry(shortPolicy()).WithStateStore(store)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			r.RecordOutcome("gpt-4o", false)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RecordOutcome should not block on a failing store's Save call")
	}
}
