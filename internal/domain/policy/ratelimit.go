// Package policy handles rate limit decisions and quota checks.
// Dimensions: use-case-id, model-id, user-id, tenant-id.
// Sliding window, token-based and request-based. 429 with Retry-After on limit.
package policy

import (
	"context"
	"sync"
	"time"
)

// LimitKey identifies the quota bucket for a request.
type LimitKey struct {
	TenantID  string
	UserID    string
	UseCaseID string
	ModelID   string
}

// RateLimit defines request and token ceilings over a sliding window.
type RateLimit struct {
	Window      time.Duration
	MaxRequests int
	MaxTokens   int
}

// Decision is the result of evaluating a quota request.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Reason     string
	Remaining  Remaining
}

// Remaining reports quota left after an allowed request.
type Remaining struct {
	Requests int
	Tokens   int
}

type usageEvent struct {
	at     time.Time
	tokens int
}

// SlidingWindowLimiter is an in-memory rate limiter suitable for single-process
// deployments and tests. Distributed deployments should back this interface with Redis.
type SlidingWindowLimiter struct {
	mu     sync.Mutex
	events map[LimitKey][]usageEvent
	now    func() time.Time
}

// NewSlidingWindowLimiter creates an empty limiter.
func NewSlidingWindowLimiter() *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		events: map[LimitKey][]usageEvent{},
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Allow records a request if it fits inside limit.
func (l *SlidingWindowLimiter) Allow(_ context.Context, key LimitKey, limit RateLimit, tokens int) Decision {
	if limit.Window <= 0 {
		return Decision{Allowed: true}
	}
	if tokens < 0 {
		tokens = 0
	}

	now := l.now()
	cutoff := now.Add(-limit.Window)

	l.mu.Lock()
	defer l.mu.Unlock()

	events := prune(l.events[key], cutoff)
	requests := len(events)
	usedTokens := 0
	for _, ev := range events {
		usedTokens += ev.tokens
	}

	if limit.MaxRequests > 0 && requests+1 > limit.MaxRequests {
		return denied("request_limit_exceeded", retryAfter(events, cutoff), limit, requests, usedTokens)
	}
	if limit.MaxTokens > 0 && usedTokens+tokens > limit.MaxTokens {
		return denied("token_limit_exceeded", retryAfter(events, cutoff), limit, requests, usedTokens)
	}

	events = append(events, usageEvent{at: now, tokens: tokens})
	l.events[key] = events

	return Decision{
		Allowed: true,
		Remaining: Remaining{
			Requests: remaining(limit.MaxRequests, len(events)),
			Tokens:   remaining(limit.MaxTokens, usedTokens+tokens),
		},
	}
}

func prune(events []usageEvent, cutoff time.Time) []usageEvent {
	i := 0
	for ; i < len(events); i++ {
		if events[i].at.After(cutoff) {
			break
		}
	}
	return events[i:]
}

func retryAfter(events []usageEvent, cutoff time.Time) time.Duration {
	if len(events) == 0 {
		return 0
	}
	d := events[0].at.Sub(cutoff)
	if d < 0 {
		return 0
	}
	return d
}

func denied(reason string, retry time.Duration, limit RateLimit, requests, tokens int) Decision {
	return Decision{
		Allowed:    false,
		RetryAfter: retry,
		Reason:     reason,
		Remaining: Remaining{
			Requests: remaining(limit.MaxRequests, requests),
			Tokens:   remaining(limit.MaxTokens, tokens),
		},
	}
}

func remaining(limit, used int) int {
	if limit <= 0 {
		return 0
	}
	left := limit - used
	if left < 0 {
		return 0
	}
	return left
}