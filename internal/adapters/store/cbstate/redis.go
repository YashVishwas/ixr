// Package cbstate provides a Redis-backed store for circuit breaker state,
// enabling all ixr instances in a cluster to share breaker state in real time.
// This is a compile-safe stub — replace with a real Redis implementation when available.
package cbstate

import "errors"

var errNotConnected = errors.New("cbstate: Redis not connected")

// Redis is a stub circuit-breaker state store.
// All methods return errNotConnected until wired to a real Redis cluster.
type Redis struct{}

// Load returns 0 (StateClosed) for every model; the in-process Breaker is authoritative.
func (r *Redis) Load(_ string) int { return 0 }

// Save is a no-op stub.
func (r *Redis) Save(_ string, _ int) error { return errNotConnected }
