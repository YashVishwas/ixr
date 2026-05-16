package usageforecast

import (
	"context"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestServiceForecastProjectsLimitOverage(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		err := store.Add(context.Background(), Observation{
			Timestamp: now.Add(time.Duration(-4+i) * time.Hour),
			UserID:    "u1",
			Model:     "gpt-4o",
			TokensIn:  60,
			TokensOut: 40,
		})
		if err != nil {
			t.Fatalf("add observation: %v", err)
		}
	}

	svc := NewService(store, MovingAverageForecaster{Lookback: 4})
	svc.now = func() time.Time { return now }

	resp, err := svc.Forecast(context.Background(), schema.TokenForecastRequest{
		UserID:         "u1",
		Model:          "gpt-4o",
		Window:         4 * time.Hour,
		Horizon:        2 * time.Hour,
		Bucket:         time.Hour,
		FreeTokenLimit: 550,
	})
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if resp.ConsumedTokens != 400 {
		t.Fatalf("consumed tokens: got %d, want 400", resp.ConsumedTokens)
	}
	if resp.ProjectedTokens != 200 {
		t.Fatalf("projected tokens: got %d, want 200", resp.ProjectedTokens)
	}
	if !resp.ProjectedOverLimit {
		t.Fatal("expected projected over limit")
	}
	if resp.FreeTokensRemaining != 150 {
		t.Fatalf("free remaining: got %d, want 150", resp.FreeTokensRemaining)
	}
}

func TestRecorderStoresUserScopedSuccessfulEvents(t *testing.T) {
	store := NewMemoryStore()
	recorder := NewRecorder(store)
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

	err := recorder.OnEvent(context.Background(), &schema.CallEvent{
		Timestamp: now,
		UserID:    "u1",
		Model:     "gpt-4o",
		TokensIn:  10,
		TokensOut: 5,
	})
	if err != nil {
		t.Fatalf("record event: %v", err)
	}
	_ = recorder.OnEvent(context.Background(), &schema.CallEvent{
		Timestamp: now,
		Model:     "gpt-4o",
		TokensIn:  100,
		TokensOut: 100,
	})

	got, err := store.Query(context.Background(), Query{UserID: "u1"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("observations: got %d, want 1", len(got))
	}
	if got[0].TotalTokens() != 15 {
		t.Fatalf("total tokens: got %d, want 15", got[0].TotalTokens())
	}
}
