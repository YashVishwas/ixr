package app

import (
	"context"
	"errors"
	"time"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// RetryPolicy controls retry behavior for provider execution.
type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
}

// RouteProvider is one execution target in primary/fallback order.
type RouteProvider struct {
	Model    string
	Provider provider.Provider
}

// ExecuteWithFallback tries the primary route, then fallbacks, retrying each route.
func ExecuteWithFallback(ctx context.Context, req *schema.RequestEnvelope, routes []RouteProvider, policy RetryPolicy) (*schema.ResponseEnvelope, RouteProvider, error) {
	if len(routes) == 0 {
		return nil, RouteProvider{}, errors.New("no provider routes")
	}
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	var lastErr error
	for _, route := range routes {
		if route.Provider == nil {
			continue
		}
		callReq := *req
		callReq.Model = route.Model
		for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
			resp, err := route.Provider.Chat(ctx, &callReq)
			if err == nil {
				return resp, route, nil
			}
			lastErr = err
			if policy.Backoff > 0 {
				timer := time.NewTimer(policy.Backoff)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, route, ctx.Err()
				}
			}
		}
	}
	return nil, RouteProvider{}, lastErr
}
