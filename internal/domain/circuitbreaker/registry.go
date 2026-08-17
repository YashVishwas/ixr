package circuitbreaker

import (
	"context"
	"sync"
	"time"
)

// StateStore lets circuit breaker state be shared across ixr instances
// behind a load balancer, so a model degradation one replica discovers is
// visible to all of them instead of each replica having to independently
// rediscover it through its own failed requests. Optional — Registry
// behaves exactly as before (fully local, in-memory, single-instance) when
// none is configured, matching the "unconfigured state must be equivalent
// to the pre-feature state" constraint.
//
// Load/Save take a context and return an error rather than blocking or
// panicking so a slow or unreachable backend degrades to "proceed with
// local state only" — never a hang that affects the caller. Registry
// treats any error from either method as exactly that: proceed locally.
type StateStore interface {
	Load(ctx context.Context, model string) (State, error)
	Save(ctx context.Context, model string, state State) error
}

// stateStoreTimeout bounds every StateStore call so a slow backend can
// never turn into an unbounded hang in (or near) the request path.
const stateStoreTimeout = 200 * time.Millisecond

// Registry manages one Breaker per model, creating entries on first use.
// Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	policy   Policy
	store    StateStore
}

// NewRegistry creates a Registry that applies p to every model.
func NewRegistry(p Policy) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		policy:   p,
	}
}

// WithStateStore attaches a shared StateStore so breaker state seeds from
// and propagates to other ixr instances. Returns r for chaining.
func (r *Registry) WithStateStore(store StateStore) *Registry {
	r.store = store
	return r
}

func (r *Registry) get(model string) *Breaker {
	r.mu.RLock()
	b, ok := r.breakers[model]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok = r.breakers[model]; ok {
		return b
	}
	b = New(r.policy)
	if r.store != nil {
		// Seeding happens once per model per process lifetime (the first
		// time this instance sees the model at all), not per request, so
		// a synchronous, short-timeout call here doesn't touch the hot
		// path in any steady-state sense.
		ctx, cancel := context.WithTimeout(context.Background(), stateStoreTimeout)
		if state, err := r.store.Load(ctx, model); err == nil {
			b.setState(state)
		}
		cancel()
	}
	r.breakers[model] = b
	return b
}

// IsAllowed returns true when requests should be forwarded to model.
func (r *Registry) IsAllowed(model string) bool {
	return r.get(model).Allow()
}

// RecordOutcome records a success or failure for model, updating its
// circuit state. When a shared StateStore is configured and this outcome
// actually changed the breaker's state (Closed<->Open<->HalfOpen — not
// every call, most calls don't transition anything), the new state is
// pushed to the store asynchronously: state transitions are rare compared
// to request volume, but RecordOutcome itself runs in the response path,
// so a synchronous Save here would let a slow shared backend add latency
// to every caller regardless of who's actually calling it.
func (r *Registry) RecordOutcome(model string, success bool) {
	b := r.get(model)
	before := b.CurrentState()
	if success {
		b.RecordSuccess()
	} else {
		b.RecordFailure()
	}

	if r.store == nil {
		return
	}
	after := b.CurrentState()
	if after == before {
		return
	}
	store := r.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), stateStoreTimeout)
		defer cancel()
		_ = store.Save(ctx, model, after)
	}()
}
