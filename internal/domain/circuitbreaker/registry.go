package circuitbreaker

import "sync"

// Registry manages one Breaker per model, creating entries on first use.
// Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	policy   Policy
}

// NewRegistry creates a Registry that applies p to every model.
func NewRegistry(p Policy) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		policy:   p,
	}
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
	r.breakers[model] = b
	return b
}

// IsAllowed returns true when requests should be forwarded to model.
func (r *Registry) IsAllowed(model string) bool {
	return r.get(model).Allow()
}

// RecordOutcome records a success or failure for model, updating its circuit state.
func (r *Registry) RecordOutcome(model string, success bool) {
	b := r.get(model)
	if success {
		b.RecordSuccess()
	} else {
		b.RecordFailure()
	}
}
