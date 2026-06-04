package policystore

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/store"
)

var errNotConnected = errors.New("redis policystore: not connected")

// Redis provides fast reads of policy weights on the hot routing path.
// Not wired until Redis is available; returns errNotConnected on all calls.
type Redis struct{ addr string }

func NewRedis(addr string) *Redis { return &Redis{addr: addr} }

func (r *Redis) GetPolicy(_ context.Context, _ string) (store.RoutingPolicy, error) {
	return store.RoutingPolicy{}, errNotConnected
}

func (r *Redis) SetPolicy(_ context.Context, _ store.RoutingPolicy) error { return errNotConnected }
