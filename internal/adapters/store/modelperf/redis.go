package modelperf

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/store"
)

var errNotConnected = errors.New("redis modelperf: not connected — use NewMemory() or wire a real Redis client")

// Redis is the hot-cache ModelPerfStore backed by Redis.
// Not wired until Redis is available; returns errNotConnected on all calls.
type Redis struct{ addr string }

func NewRedis(addr, _ string, _ int) *Redis { return &Redis{addr: addr} }

func (r *Redis) Get(_ context.Context, _, _ string) (store.ModelStats, error) {
	return store.ModelStats{}, errNotConnected
}

func (r *Redis) Upsert(_ context.Context, _ store.ModelStats) error { return errNotConnected }

func (r *Redis) List(_ context.Context, _ string) ([]store.ModelStats, error) {
	return nil, errNotConnected
}
