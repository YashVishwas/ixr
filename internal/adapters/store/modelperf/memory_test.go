package modelperf

import (
	"context"
	"sync"
	"testing"

	"github.com/YashVishwas/ixr/pkg/store"
)

func TestMemory_GetMissing(t *testing.T) {
	m := NewMemory()
	s, err := m.Get(context.Background(), "gpt-4o", "coding")
	if err != nil {
		t.Fatalf("Get on empty store: %v", err)
	}
	if s.Model != "gpt-4o" || s.Intent != "coding" {
		t.Errorf("zero-value stats should preserve model/intent, got %+v", s)
	}
}

func TestMemory_UpsertAndGet(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	s := store.ModelStats{
		Model:        "claude-3",
		Intent:       "reasoning",
		SuccessRate:  1.0,
		P50LatencyMS: 400,
		P95LatencyMS: 900,
		MedianCostUSD: 0.01,
	}
	if err := m.Upsert(ctx, s); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := m.Get(ctx, "claude-3", "reasoning")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SampleCount != 1 {
		t.Errorf("SampleCount: got %d, want 1", got.SampleCount)
	}
	if got.P50LatencyMS != 400 {
		t.Errorf("P50LatencyMS: got %f, want 400", got.P50LatencyMS)
	}
}

func TestMemory_EMAConverges(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	for i := 0; i < 30; i++ {
		_ = m.Upsert(ctx, store.ModelStats{
			Model:        "m",
			Intent:       "i",
			P50LatencyMS: 100,
			SuccessRate:  1.0,
		})
	}

	got, _ := m.Get(ctx, "m", "i")
	if got.P50LatencyMS < 95 || got.P50LatencyMS > 105 {
		t.Errorf("EMA should converge near 100 after 30 samples, got %f", got.P50LatencyMS)
	}
}

func TestMemory_SuccessRateSlidingWindow(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	// 5 successes
	for i := 0; i < 5; i++ {
		_ = m.Upsert(ctx, store.ModelStats{Model: "m2", Intent: "i2", SuccessRate: 1.0})
	}
	// 5 failures
	for i := 0; i < 5; i++ {
		_ = m.Upsert(ctx, store.ModelStats{Model: "m2", Intent: "i2", SuccessRate: 0.0})
	}

	got, _ := m.Get(ctx, "m2", "i2")
	if got.SuccessRate < 0.45 || got.SuccessRate > 0.55 {
		t.Errorf("success rate should be ~0.5 after 5 success + 5 failure, got %f", got.SuccessRate)
	}
}

func TestMemory_ConcurrentUpsert(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Upsert(ctx, store.ModelStats{Model: "concurrent", Intent: "test", SuccessRate: 1.0, P50LatencyMS: 200})
		}()
	}
	wg.Wait()

	got, _ := m.Get(ctx, "concurrent", "test")
	if got.SampleCount != 50 {
		t.Errorf("SampleCount: got %d, want 50", got.SampleCount)
	}
}

func TestMemory_List(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_ = m.Upsert(ctx, store.ModelStats{Model: "a", Intent: "i1", SuccessRate: 1.0})
	_ = m.Upsert(ctx, store.ModelStats{Model: "a", Intent: "i2", SuccessRate: 1.0})
	_ = m.Upsert(ctx, store.ModelStats{Model: "b", Intent: "i1", SuccessRate: 1.0})

	all, err := m.List(ctx, "")
	if err != nil || len(all) != 3 {
		t.Errorf("List all: got %d items, want 3 (err=%v)", len(all), err)
	}

	filtered, err := m.List(ctx, "a")
	if err != nil || len(filtered) != 2 {
		t.Errorf("List filtered by 'a': got %d items, want 2 (err=%v)", len(filtered), err)
	}
}
