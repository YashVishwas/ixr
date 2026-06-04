// Package store defines the persistence interfaces for ixr's intelligence layer.
package store

import "context"

// ModelStats holds rolling performance statistics for a (model, intent) pair.
type ModelStats struct {
	Model         string
	Intent        string
	SuccessRate   float64
	P50LatencyMS  float64
	P95LatencyMS  float64
	MedianCostUSD float64
	SampleCount   int64
	UpdatedAt     int64 // unix nano
}

// ModelPerfStore persists and retrieves per-(model, intent) performance statistics.
// All methods must be safe for concurrent use.
type ModelPerfStore interface {
	// Get returns current stats for the (model, intent) pair.
	// Returns zero-value ModelStats and no error when the key is absent.
	Get(ctx context.Context, model, intent string) (ModelStats, error)
	// Upsert merges a new observation into the running stats atomically.
	Upsert(ctx context.Context, stats ModelStats) error
	// List returns all stored stats, optionally filtered by model (empty = all).
	List(ctx context.Context, model string) ([]ModelStats, error)
}
