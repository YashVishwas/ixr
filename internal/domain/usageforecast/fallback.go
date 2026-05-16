package usageforecast

import (
	"context"
	"log/slog"
	"time"
)

// FallbackForecaster uses primary first and falls back when primary fails.
type FallbackForecaster struct {
	Primary  Forecaster
	Fallback Forecaster
}

// Forecast returns the primary forecast when possible, otherwise fallback output.
func (f FallbackForecaster) Forecast(ctx context.Context, history []Point, horizon int, bucket time.Duration) ([]ForecastPoint, error) {
	if f.Primary != nil {
		points, err := f.Primary.Forecast(ctx, history, horizon, bucket)
		if err == nil {
			return points, nil
		}
		slog.Warn("usage forecast primary failed, falling back", "err", err)
	}
	if f.Fallback == nil {
		f.Fallback = MovingAverageForecaster{Lookback: 24}
	}
	return f.Fallback.Forecast(ctx, history, horizon, bucket)
}
