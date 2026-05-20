package usageforecast

import (
	"context"
	"errors"
	"math"
	"time"
)

// Point is one time-series sample.
type Point struct {
	Timestamp time.Time
	Value     float64
}

// ForecastPoint is one predicted time-series value.
type ForecastPoint struct {
	Timestamp time.Time
	Value     float64
}

// Forecaster predicts future token usage buckets from historical buckets.
type Forecaster interface {
	Forecast(ctx context.Context, history []Point, horizon int, bucket time.Duration) ([]ForecastPoint, error)
}

// MovingAverageForecaster is the local fallback when no TimesFM service is configured.
type MovingAverageForecaster struct {
	Lookback int
}

// Forecast projects recent average usage. It intentionally keeps the fallback
// simple; production zero-shot accuracy should come from the TimesFM adapter.
func (f MovingAverageForecaster) Forecast(_ context.Context, history []Point, horizon int, bucket time.Duration) ([]ForecastPoint, error) {
	if horizon <= 0 {
		return nil, errors.New("horizon must be positive")
	}
	if bucket <= 0 {
		return nil, errors.New("bucket must be positive")
	}

	lookback := f.Lookback
	if lookback <= 0 || lookback > len(history) {
		lookback = len(history)
	}

	var avg float64
	if lookback > 0 {
		for _, p := range history[len(history)-lookback:] {
			avg += p.Value
		}
		avg /= float64(lookback)
	}

	start := time.Now().UTC()
	if len(history) > 0 {
		start = history[len(history)-1].Timestamp
	}
	out := make([]ForecastPoint, horizon)
	for i := range out {
		out[i] = ForecastPoint{
			Timestamp: start.Add(time.Duration(i+1) * bucket),
			Value:     math.Max(0, avg),
		}
	}
	return out, nil
}
