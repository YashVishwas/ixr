package modelperf

import (
	"context"
	"errors"
	"sync"
)

// RedisStore is the hot cache adapter seam. The current implementation is an
// in-memory stand-in so the scoring engine can be wired before adding a Redis client dependency.
type RedisStore struct {
	mu      sync.RWMutex
	records map[string]Record
}

func NewRedisStore() *RedisStore {
	return &RedisStore{records: map[string]Record{}}
}

func (s *RedisStore) Get(_ context.Context, model, intent string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key(model, intent)]
	if !ok {
		return Record{}, errors.New("model performance record not found")
	}
	return rec, nil
}

func (s *RedisStore) Put(_ context.Context, record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key(record.Model, record.Intent)] = record
	return nil
}

func key(model, intent string) string {
	return model + "\x00" + intent
}

// redis is the hot cache for scoring engine reads (target: < 1ms).
// All data is pre-computed and written here by the telemetry pipeline.
