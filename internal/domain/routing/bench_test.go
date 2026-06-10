package routing

import (
	"testing"

	"github.com/YashVishwas/ixr/pkg/store"
)

func BenchmarkRoute_Auto(b *testing.B) {
	hint := TaskHint{MaxCostUSDPer1M: 10, PromptChars: 1000}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Route(hint)
	}
}

func BenchmarkScore_WithStats(b *testing.B) {
	cat := Catalog()
	if len(cat) == 0 {
		b.Skip("empty catalog")
	}
	card := cat[0]
	hint := TaskHint{MaxCostUSDPer1M: 10}
	stats := store.ModelStats{SuccessRate: 0.99, P50LatencyMS: 500, P95LatencyMS: 900, MedianCostUSD: 0.001, SampleCount: 100}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Score(card, hint, stats, [3]float64{0.4, 0.4, 0.2}, InputShare(1000))
	}
}

func BenchmarkFilter(b *testing.B) {
	hint := TaskHint{MaxCostUSDPer1M: 5}
	cat := Catalog()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Filter(cat, hint, nil)
	}
}
