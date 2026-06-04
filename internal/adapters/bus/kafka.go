package bus

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var errKafkaNotConnected = errors.New("Kafka bus: not connected (add github.com/segmentio/kafka-go and wire a real producer)")

// Kafka publishes CallEvents to a Kafka topic as JSON messages.
// This is a compile-safe stub.
type Kafka struct {
	brokers []string
	topic   string
}

// NewKafka creates a stub Kafka bus.
func NewKafka(brokers []string, topic string) *Kafka {
	return &Kafka{brokers: brokers, topic: topic}
}

func (k *Kafka) Subscribe(_ plugin.EventConsumer) {}

func (k *Kafka) Publish(_ context.Context, _ *schema.CallEvent) error {
	return errKafkaNotConnected
}
