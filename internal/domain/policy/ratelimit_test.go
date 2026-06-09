package policy

import (
	"context"
	"testing"
	"time"
)

func TestSlidingWindowLimiterRequestLimit(t *testing.T) {
	limiter := NewSlidingWindowLimiter()
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	key := LimitKey{TenantID: "t1", UserID: "u1"}
	limit := RateLimit{Window: time.Minute, MaxRequests: 2}

	if got := limiter.Allow(context.Background(), key, limit, 10); !got.Allowed {
		t.Fatalf("first request denied: %+v", got)
	}
	if got := limiter.Allow(context.Background(), key, limit, 10); !got.Allowed {
		t.Fatalf("second request denied: %+v", got)
	}
	if got := limiter.Allow(context.Background(), key, limit, 10); got.Allowed || got.Reason != "request_limit_exceeded" {
		t.Fatalf("third request: got %+v, want request denial", got)
	}
}

func TestSlidingWindowLimiterPrunesOldEvents(t *testing.T) {
	limiter := NewSlidingWindowLimiter()
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	key := LimitKey{TenantID: "t1", UserID: "u1"}
	limit := RateLimit{Window: time.Minute, MaxRequests: 1}
	if got := limiter.Allow(context.Background(), key, limit, 10); !got.Allowed {
		t.Fatalf("first request denied: %+v", got)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if got := limiter.Allow(context.Background(), key, limit, 10); !got.Allowed {
		t.Fatalf("request after window denied: %+v", got)
	}
}

func TestSlidingWindowLimiterTokenLimit(t *testing.T) {
	limiter := NewSlidingWindowLimiter()
	limiter.now = func() time.Time { return time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC) }

	got := limiter.Allow(context.Background(), LimitKey{TenantID: "t1"}, RateLimit{
		Window:    time.Minute,
		MaxTokens: 100,
	}, 120)
	if got.Allowed || got.Reason != "token_limit_exceeded" {
		t.Fatalf("token limit: got %+v", got)
	}
}