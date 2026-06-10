// Package circuitstate stores shared circuit-breaker state.
package circuitstate

import (
	"context"
	"errors"
	"sync"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
)

// RedisStore is the shared circuit-state adapter seam. It is in-memory until a
// Redis client is introduced.
type RedisStore struct {
	mu     sync.RWMutex
	states map[string]circuitbreaker.State
}

func NewRedisStore() *RedisStore {
	return &RedisStore{states: map[string]circuitbreaker.State{}}
}

func (s *RedisStore) Get(_ context.Context, key string) (circuitbreaker.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[key]
	if !ok {
		return circuitbreaker.Closed, errors.New("circuit state not found")
	}
	return state, nil
}

func (s *RedisStore) Put(_ context.Context, key string, state circuitbreaker.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[key] = state
	return nil
}
