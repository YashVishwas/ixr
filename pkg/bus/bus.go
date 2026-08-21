// Package bus defines the event bus interface.
// ixr ships two implementations: an in-memory bus for single-process delivery
// and a webhook bus for fanning events out to external services. Additional
// backends (Kafka, NATS, etc.) can implement this interface without touching
// callers, but we don't carry unproven stub adapters for backends nobody has
// asked for — add one when a real integration needs it.
// Swapping implementations never changes code that publishes or subscribes.
package bus

import (
	"context"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Bus delivers CallEvents to all registered EventConsumers.
// Publish is non-blocking; a slow consumer must not block the request path.
type Bus interface {
	// Publish enqueues ev for delivery to all subscribers.
	Publish(ctx context.Context, ev *schema.CallEvent) error
	// Subscribe registers c to receive all future events.
	// Must be called before the first Publish.
	Subscribe(c plugin.EventConsumer)
}
