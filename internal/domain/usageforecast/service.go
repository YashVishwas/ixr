package usageforecast

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

const (
	DefaultWindow  = 7 * 24 * time.Hour
	DefaultHorizon = 24 * time.Hour
	DefaultBucket  = time.Hour
)

// Service coordinates usage history and forecasting.
type Service struct {
	store      Store
	forecaster Forecaster
	now        func() time.Time
}

// NewService creates a usage forecasting service.
func NewService(store Store, forecaster Forecaster) *Service {
	if forecaster == nil {
		forecaster = MovingAverageForecaster{Lookback: 24}
	}
	return &Service{
		store:      store,
		forecaster: forecaster,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Forecast returns observed token consumption plus a future token projection.
func (s *Service) Forecast(ctx context.Context, req schema.TokenForecastRequest) (*schema.TokenForecastResponse, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("usage forecast service is not configured")
	}
	if req.UserID == "" {
		return nil, errors.New("user_id is required")
	}
	if req.Window <= 0 {
		req.Window = DefaultWindow
	}
	if req.Horizon <= 0 {
		req.Horizon = DefaultHorizon
	}
	if req.Bucket <= 0 {
		req.Bucket = DefaultBucket
	}
	if req.Horizon < req.Bucket {
		return nil, errors.New("horizon must be greater than or equal to bucket")
	}

	now := s.now()
	obs, err := s.store.Query(ctx, Query{
		UserID: req.UserID,
		Model:  req.Model,
		Since:  now.Add(-req.Window),
		Until:  now.Add(time.Nanosecond),
	})
	if err != nil {
		return nil, err
	}

	buckets, consumed := bucketObservations(obs, now.Add(-req.Window), now, req.Bucket)
	steps := int(math.Ceil(float64(req.Horizon) / float64(req.Bucket)))
	forecast, err := s.forecaster.Forecast(ctx, buckets, steps, req.Bucket)
	if err != nil {
		return nil, err
	}

	points := make([]schema.TokenUsagePoint, len(forecast))
	projected := 0
	for i, p := range forecast {
		tokens := int(math.Round(math.Max(0, p.Value)))
		points[i] = schema.TokenUsagePoint{Timestamp: p.Timestamp, Tokens: tokens}
		projected += tokens
	}

	remaining := 0
	if req.FreeTokenLimit > 0 {
		remaining = req.FreeTokenLimit - consumed
		if remaining < 0 {
			remaining = 0
		}
	}

	return &schema.TokenForecastResponse{
		UserID:                   req.UserID,
		Model:                    req.Model,
		WindowHours:              req.Window.Hours(),
		HorizonHours:             req.Horizon.Hours(),
		BucketMinutes:            req.Bucket.Minutes(),
		ConsumedTokens:           consumed,
		FreeTokenLimit:           req.FreeTokenLimit,
		FreeTokensRemaining:      remaining,
		CurrentRateTokensPerHour: currentRate(consumed, req.Window),
		ProjectedTokens:          projected,
		ProjectedTotalTokens:     consumed + projected,
		ProjectedOverLimit:       req.FreeTokenLimit > 0 && consumed+projected > req.FreeTokenLimit,
		Forecast:                 points,
	}, nil
}

func bucketObservations(obs []Observation, start, end time.Time, bucket time.Duration) ([]Point, int) {
	if bucket <= 0 || !end.After(start) {
		return nil, 0
	}
	steps := int(math.Ceil(float64(end.Sub(start)) / float64(bucket)))
	points := make([]Point, steps)
	for i := range points {
		points[i].Timestamp = start.Add(time.Duration(i) * bucket)
	}

	total := 0
	for _, o := range obs {
		if o.Timestamp.Before(start) || !o.Timestamp.Before(end.Add(time.Nanosecond)) {
			continue
		}
		tokens := o.TotalTokens()
		total += tokens
		idx := int(o.Timestamp.Sub(start) / bucket)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(points) {
			idx = len(points) - 1
		}
		points[idx].Value += float64(tokens)
	}
	return points, total
}

func currentRate(consumed int, window time.Duration) float64 {
	if consumed == 0 || window <= 0 {
		return 0
	}
	return float64(consumed) / window.Hours()
}
