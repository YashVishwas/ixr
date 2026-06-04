package bus

import (
	"context"
	"log/slog"

	pkgbus "github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Fanout dispatches every event to all child buses concurrently.
// This is the standard way to combine the in-memory bus with webhook or
// external streaming buses (Kafka, NATS, etc.).
type Fanout struct {
	children []pkgbus.Bus
}

// NewFanout creates a Fanout that dispatches to all provided buses.
func NewFanout(children ...pkgbus.Bus) *Fanout {
	return &Fanout{children: children}
}

// Subscribe registers c on every child bus.
func (f *Fanout) Subscribe(c plugin.EventConsumer) {
	for _, b := range f.children {
		b.Subscribe(c)
	}
}

// Publish sends ev to every child bus concurrently.
// All errors are logged; the first error (if any) is returned.
func (f *Fanout) Publish(ctx context.Context, ev *schema.CallEvent) error {
	var firstErr error
	for _, b := range f.children {
		if err := b.Publish(ctx, ev); err != nil {
			slog.Error("fanout bus: child publish error", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
