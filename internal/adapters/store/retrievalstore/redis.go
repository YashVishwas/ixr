// Package retrievalstore provides a shared, multi-instance-safe Backend for
// internal/domain/retrieval.Store, so the reversible-compression retrieval
// window survives being resolved by a different ixr replica than the one
// that wrote it — the default in-memory backend can't do that behind a
// load balancer with more than one replica.
package retrievalstore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a retrieval.Backend backed by a Redis (or Redis-API-compatible)
// server. Every entry is namespaced under a "retrieval:" key prefix so it
// can share a Redis instance/database with other ixr state without key
// collisions.
//
// Redis's own key expiry (SET ... EX) replaces the in-memory backend's
// maxSize eviction — there's no equivalent "how many keys does this
// deployment have" concept worth tracking per-instance, so Len() is not
// implemented (Store.Len() reports -1 for backends that don't provide it).
type Redis struct {
	client *redis.Client
}

// New creates a Redis-backed retrieval.Backend from an already-configured
// client. The caller owns the client's lifecycle (creation and Close).
func New(client *redis.Client) *Redis {
	return &Redis{client: client}
}

const keyPrefix = "retrieval:"

func (r *Redis) Put(ctx context.Context, id, content string, ttl time.Duration) error {
	// A Redis TTL of 0 means "no expiry" already, matching Store's own
	// ttl<=0 convention — no translation needed.
	if ttl < 0 {
		ttl = 0
	}
	return r.client.Set(ctx, keyPrefix+id, content, ttl).Err()
}

func (r *Redis) Get(ctx context.Context, id string) (string, bool, error) {
	val, err := r.client.Get(ctx, keyPrefix+id).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}
