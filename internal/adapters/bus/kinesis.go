package bus

import (
	"context"
	"errors"

	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

var errKinesisNotConnected = errors.New("Kinesis bus: not connected (configure AWS credentials and region)")

// Kinesis publishes CallEvents to an AWS Kinesis Data Stream.
// This is a compile-safe stub. A real implementation would use the AWS SDK v2
// or raw SigV4-signed HTTP calls to kinesis.<region>.amazonaws.com.
type Kinesis struct {
	region string
	stream string
}

// NewKinesis creates a stub Kinesis bus.
func NewKinesis(region, stream string) *Kinesis {
	return &Kinesis{region: region, stream: stream}
}

func (k *Kinesis) Subscribe(_ plugin.EventConsumer) {}

func (k *Kinesis) Publish(_ context.Context, _ *schema.CallEvent) error {
	return errKinesisNotConnected
}
