package timesfm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/usageforecast"
)

// HTTPForecaster calls a remote TimesFM forecasting service over HTTP.
//
// Expected service contract:
// POST /v1/forecast {"horizon": 24, "inputs": [[1, 2, 3]]}
// -> {"point_forecast": [[4, 5, 6]], "latency_ms": 12}
type HTTPForecaster struct {
	baseURL string
	client  *http.Client
}

// NewHTTPForecaster creates a TimesFM HTTP adapter.
func NewHTTPForecaster(baseURL string, timeout time.Duration) *HTTPForecaster {
	if timeout <= 0 {
		timeout = 20 * time.Millisecond
	}
	return &HTTPForecaster{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

type forecastRequest struct {
	Horizon int         `json:"horizon"`
	Inputs  [][]float64 `json:"inputs"`
}

type forecastResponse struct {
	PointForecast [][]float64 `json:"point_forecast"`
	LatencyMS     float64     `json:"latency_ms,omitempty"`
}

// Forecast sends the historical token buckets to a TimesFM service.
func (f *HTTPForecaster) Forecast(ctx context.Context, history []usageforecast.Point, horizon int, bucket time.Duration) ([]usageforecast.ForecastPoint, error) {
	if f == nil || f.baseURL == "" {
		return nil, errors.New("timesfm base URL is required")
	}
	if horizon <= 0 {
		return nil, errors.New("horizon must be positive")
	}

	input := make([]float64, len(history))
	for i, p := range history {
		input[i] = p.Value
	}
	body, err := json.Marshal(forecastRequest{
		Horizon: horizon,
		Inputs:  [][]float64{input},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/v1/forecast", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("timesfm forecast failed with status %d", resp.StatusCode)
	}

	var decoded forecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.PointForecast) == 0 || len(decoded.PointForecast[0]) < horizon {
		return nil, errors.New("timesfm response missing forecast points")
	}

	start := time.Now().UTC()
	if len(history) > 0 {
		start = history[len(history)-1].Timestamp
	}
	out := make([]usageforecast.ForecastPoint, horizon)
	for i := range out {
		out[i] = usageforecast.ForecastPoint{
			Timestamp: start.Add(time.Duration(i+1) * bucket),
			Value:     decoded.PointForecast[0][i],
		}
	}
	return out, nil
}
