package modelperf

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/store"
)

const emaAlpha = 0.1 // exponential moving average smoothing factor

// Memory is the primary ModelPerfStore implementation for single-node deployments.
// Latency and cost are updated with an EMA (α=0.1).
// Success rate uses a sliding window of the last 100 samples.
type Memory struct {
	mu    sync.RWMutex
	stats map[string]*entry
}

type entry struct {
	store.ModelStats
	recentResults [100]bool
	resultHead    int
	resultCount   int
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{stats: make(map[string]*entry)}
}

func key(model, intent string) string { return model + ":" + intent }

func (m *Memory) Get(_ context.Context, model, intent string) (store.ModelStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.stats[key(model, intent)]
	if !ok {
		return store.ModelStats{Model: model, Intent: intent}, nil
	}
	return e.ModelStats, nil
}

func (m *Memory) Upsert(_ context.Context, s store.ModelStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(s.Model, s.Intent)
	e, ok := m.stats[k]
	if !ok {
		e = &entry{}
		e.Model = s.Model
		e.Intent = s.Intent
		m.stats[k] = e
	}

	e.SampleCount++
	e.UpdatedAt = time.Now().UnixNano()

	// EMA updates for latency and cost.
	if e.SampleCount == 1 {
		e.P50LatencyMS = s.P50LatencyMS
		e.P95LatencyMS = s.P95LatencyMS
		e.MedianCostUSD = s.MedianCostUSD
	} else {
		e.P50LatencyMS = emaAlpha*s.P50LatencyMS + (1-emaAlpha)*e.P50LatencyMS
		e.P95LatencyMS = emaAlpha*s.P95LatencyMS + (1-emaAlpha)*e.P95LatencyMS
		e.MedianCostUSD = emaAlpha*s.MedianCostUSD + (1-emaAlpha)*e.MedianCostUSD
	}

	// Sliding success window.
	idx := e.resultHead % len(e.recentResults)
	e.recentResults[idx] = s.SuccessRate >= 1.0
	e.resultHead++
	n := e.resultCount
	if n < len(e.recentResults) {
		n++
		e.resultCount = n
	}
	successes := 0
	for i := 0; i < n; i++ {
		if e.recentResults[i] {
			successes++
		}
	}
	e.SuccessRate = float64(successes) / float64(n)

	return nil
}

func (m *Memory) List(_ context.Context, model string) ([]store.ModelStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []store.ModelStats
	for k, e := range m.stats {
		if model != "" && !strings.HasPrefix(k, model+":") {
			continue
		}
		out = append(out, e.ModelStats)
	}
	return out, nil
}
