package bus

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var errNATSNotConnected = errors.New("NATS bus: not connected (add github.com/nats-io/nats.go and wire a real connection)")

// NATS publishes CallEvents to a NATS subject.
// This is a compile-safe stub — replace the body of Connect() and Publish() with
// a real nats.Conn when the nats.go client is added as a dependency.
type NATS struct {
	subject string
}

// NewNATS creates a stub NATS bus targeting subject.
func NewNATS(url, subject string) *NATS {
	_ = url
	return &NATS{subject: subject}
}

func (n *NATS) Subscribe(_ plugin.EventConsumer) {}

func (n *NATS) Publish(_ context.Context, _ *schema.CallEvent) error {
	return errNATSNotConnected
}
