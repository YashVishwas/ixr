package bus

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var errPubSubNotConnected = errors.New("GCP Pub/Sub bus: not connected (add cloud.google.com/go/pubsub and authenticate via ADC)")

// PubSub publishes CallEvents to a Google Cloud Pub/Sub topic.
// This is a compile-safe stub.
type PubSub struct {
	project string
	topic   string
}

// NewPubSub creates a stub GCP Pub/Sub bus.
func NewPubSub(project, topic string) *PubSub {
	return &PubSub{project: project, topic: topic}
}

func (p *PubSub) Subscribe(_ plugin.EventConsumer) {}

func (p *PubSub) Publish(_ context.Context, _ *schema.CallEvent) error {
	return errPubSubNotConnected
}
