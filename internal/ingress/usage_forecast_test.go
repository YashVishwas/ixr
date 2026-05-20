package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/usageforecast"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestUsageForecastHandlerReturnsForecast(t *testing.T) {
	store := usageforecast.NewMemoryStore()
	now := time.Now().UTC()
	_ = store.Add(context.Background(), usageforecast.Observation{
		Timestamp: now.Add(-time.Hour),
		UserID:    "u1",
		Model:     "gpt-4o",
		TokensIn:  100,
		TokensOut: 50,
	})
	svc := usageforecast.NewService(store, usageforecast.MovingAverageForecaster{Lookback: 1})

	h := NewUsageForecastHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/v1/usage/forecast?user_id=u1&model=gpt-4o&window_hours=2&horizon_hours=1&bucket_minutes=60&free_token_limit=200", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp schema.TokenForecastResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ConsumedTokens != 150 {
		t.Fatalf("consumed tokens: got %d, want 150", resp.ConsumedTokens)
	}
	if len(resp.Forecast) != 1 {
		t.Fatalf("forecast points: got %d, want 1", len(resp.Forecast))
	}
}

func TestUsageForecastHandlerRequiresUser(t *testing.T) {
	h := NewUsageForecastHandler(usageforecast.NewService(usageforecast.NewMemoryStore(), nil))
	req := httptest.NewRequest(http.MethodGet, "/v1/usage/forecast", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestUsageForecastJobHandlerCreatesAndCompletesJob(t *testing.T) {
	store := usageforecast.NewMemoryStore()
	now := time.Now().UTC()
	_ = store.Add(context.Background(), usageforecast.Observation{
		Timestamp: now.Add(-time.Hour),
		UserID:    "u1",
		Model:     "gpt-4o",
		TokensIn:  100,
		TokensOut: 50,
	})
	svc := usageforecast.NewService(store, usageforecast.MovingAverageForecaster{Lookback: 1})
	orchestrator := usageforecast.NewJobOrchestrator(svc, usageforecast.NewMemoryJobStore(), 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orchestrator.Start(ctx, 1)

	h := NewUsageForecastJobHandler(orchestrator)
	req := httptest.NewRequest(http.MethodPost, "/v1/usage/forecast/jobs",
		strings.NewReader(`{"user_id":"u1","model":"gpt-4o","window_hours":2,"horizon_hours":1,"bucket_minutes":60}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", w.Code, w.Body.String())
	}
	var created schema.TokenForecastJob
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}

	var job *schema.TokenForecastJob
	for i := 0; i < 20; i++ {
		got, err := orchestrator.Get(context.Background(), created.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		job = got
		if job.Status == schema.ForecastJobSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != schema.ForecastJobSucceeded {
		t.Fatalf("job status: got %q, want succeeded", job.Status)
	}
	if job.Result == nil || job.Result.ConsumedTokens != 150 {
		t.Fatalf("job result: got %+v", job.Result)
	}
}
