package modelperf

import (
	"context"
	"time"
)

// Record is one hot-path model performance row.
type Record struct {
	Model        string
	Provider     string
	Intent       string
	AvgLatencyMS float64
	P50LatencyMS float64
	P95LatencyMS float64
	SuccessRate  float64
	CostPer1KIn  float64
	CostPer1KOut float64
	CircuitOpen  bool
	LastUpdated  time.Time
}

// Store reads and writes model performance records.
type Store interface {
	Get(ctx context.Context, model, intent string) (Record, error)
	Put(ctx context.Context, record Record) error
}
