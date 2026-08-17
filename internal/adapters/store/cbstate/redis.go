// Package cbstate provides a Redis-backed circuitbreaker.StateStore, so all
// ixr instances in a cluster share circuit breaker state — a model one
// replica trips is immediately excluded on every other replica too, rather
// than each one having to independently rediscover the degradation through
// its own failed requests.
package cbstate

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
)

// Redis is a circuitbreaker.StateStore backed by a Redis (or Redis-API-
// compatible) server. Every entry is namespaced under a "cbstate:" key
// prefix so it can share a Redis instance/database with other ixr state
// (e.g. internal/adapters/store/retrievalstore) without key collisions.
type Redis struct {
	client *redis.Client
	ttl    time.Duration
}

// New creates a Redis-backed StateStore from an already-configured client.
// The caller owns the client's lifecycle (creation and Close). ttl bounds
// how long a saved state is trusted before Redis expires it — a model that
// hasn't transitioned in a while (recovered, or the whole cluster restarted)
// shouldn't have a permanently stale "Open" entry outliving its relevance;
// ttl<=0 disables expiry.
func New(client *redis.Client, ttl time.Duration) *Redis {
	return &Redis{client: client, ttl: ttl}
}

const keyPrefix = "cbstate:"

func (r *Redis) Save(ctx context.Context, model string, state circuitbreaker.State) error {
	ttl := r.ttl
	if ttl < 0 {
		ttl = 0
	}
	return r.client.Set(ctx, keyPrefix+model, int(state), ttl).Err()
}

func (r *Redis) Load(ctx context.Context, model string) (circuitbreaker.State, error) {
	val, err := r.client.Get(ctx, keyPrefix+model).Result()
	if err != nil {
		return circuitbreaker.StateClosed, err
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return circuitbreaker.StateClosed, err
	}
	return circuitbreaker.State(n), nil
}
