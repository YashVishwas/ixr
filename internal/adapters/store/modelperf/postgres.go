// Package modelperf stores and retrieves per-(model, intent) performance statistics.
// postgres is the source of truth; redis is the hot cache the scoring engine reads.
package modelperf

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/store"
)

var errPGNotConnected = errors.New("postgres modelperf: not connected")

// Postgres is the source-of-truth ModelPerfStore backed by Postgres.
// Not wired until Postgres is available; returns errPGNotConnected on all calls.
type Postgres struct{ dsn string }

func NewPostgres(dsn string) *Postgres { return &Postgres{dsn: dsn} }

func (p *Postgres) Get(_ context.Context, _, _ string) (store.ModelStats, error) {
	return store.ModelStats{}, errPGNotConnected
}

func (p *Postgres) Upsert(_ context.Context, _ store.ModelStats) error { return errPGNotConnected }

func (p *Postgres) List(_ context.Context, _ string) ([]store.ModelStats, error) {
	return nil, errPGNotConnected
}
