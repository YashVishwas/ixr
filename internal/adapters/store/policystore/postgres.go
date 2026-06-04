// Package policystore stores per-intent routing weights and per-use-case configs.
package policystore

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/store"
)

var errPGNotConnected = errors.New("postgres policystore: not connected")

// Postgres is the source-of-truth PolicyStore backed by Postgres.
// Not wired until Postgres is available; returns errPGNotConnected on all calls.
type Postgres struct{ dsn string }

func NewPostgres(dsn string) *Postgres { return &Postgres{dsn: dsn} }

func (p *Postgres) GetPolicy(_ context.Context, _ string) (store.RoutingPolicy, error) {
	return store.RoutingPolicy{}, errPGNotConnected
}

func (p *Postgres) SetPolicy(_ context.Context, _ store.RoutingPolicy) error {
	return errPGNotConnected
}
