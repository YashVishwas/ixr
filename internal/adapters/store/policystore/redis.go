package policystore

import (
	"context"
	"errors"
	"sync"
)

// RedisStore is the hot policy cache adapter seam.
type RedisStore struct {
	mu      sync.RWMutex
	weights map[string]Weights
	shadow  map[string]ShadowPolicy
}

func NewRedisStore() *RedisStore {
	return &RedisStore{
		weights: map[string]Weights{},
		shadow:  map[string]ShadowPolicy{},
	}
}

func (s *RedisStore) Weights(_ context.Context, intent string) (Weights, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.weights[intent]
	if !ok {
		return Weights{}, errors.New("policy weights not found")
	}
	return w, nil
}

func (s *RedisStore) Shadow(_ context.Context, useCaseID string) (ShadowPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.shadow[useCaseID]
	if !ok {
		return ShadowPolicy{}, errors.New("shadow policy not found")
	}
	return p, nil
}

func (s *RedisStore) PutWeights(intent string, weights Weights) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.weights[intent] = weights
}

func (s *RedisStore) PutShadow(useCaseID string, policy ShadowPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shadow[useCaseID] = policy
}

// redis provides fast reads of policy weights on the hot routing path.
