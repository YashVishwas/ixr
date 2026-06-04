// Package policy handles rate limit decisions and quota checks.
// Sliding window, token-based and request-based. 429 with Retry-After on limit.
package policy

import (
	"context"
	"fmt"
	"sync"
	"time"

	cfgpkg "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Decision is the result of a rate limit check.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Remaining  int
}

// RateLimiter enforces sliding-window request and token limits.
type RateLimiter interface {
	Check(ctx context.Context, id schema.Identity, tokenCost int) Decision
	Record(ctx context.Context, id schema.Identity, tokensUsed int)
	Reload(cfg cfgpkg.RateLimitConfig)
}

type windowEntry struct {
	at     time.Time
	tokens int
}

type slidingWindow struct {
	entries []windowEntry
}

// MemoryRateLimiter is the in-process sliding-window implementation.
type MemoryRateLimiter struct {
	mu        sync.Mutex
	windows   map[string]*slidingWindow
	maxReq    int
	maxTok    int
	windowDur time.Duration
}

// NewMemoryRateLimiter creates a rate limiter.
// maxRequests and maxTokens of 0 mean unlimited.
func NewMemoryRateLimiter(maxRequests, maxTokens int, windowDur time.Duration) *MemoryRateLimiter {
	if windowDur <= 0 {
		windowDur = 60 * time.Second
	}
	return &MemoryRateLimiter{
		windows:   make(map[string]*slidingWindow),
		maxReq:    maxRequests,
		maxTok:    maxTokens,
		windowDur: windowDur,
	}
}

// Reload updates limits atomically (for hot reload).
func (m *MemoryRateLimiter) Reload(cfg cfgpkg.RateLimitConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxReq = cfg.MaxRequests
	m.maxTok = cfg.MaxTokens
	if cfg.WindowSec > 0 {
		m.windowDur = time.Duration(cfg.WindowSec) * time.Second
	}
}

// Check evaluates whether the request should be allowed.
func (m *MemoryRateLimiter) Check(_ context.Context, id schema.Identity, tokenCost int) Decision {
	if m.maxReq == 0 && m.maxTok == 0 {
		return Decision{Allowed: true}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := windowKey(id)
	now := time.Now()
	cutoff := now.Add(-m.windowDur)

	w := m.getOrCreate(key)
	purge(w, cutoff)

	reqCount := len(w.entries)
	tokCount := sumTokens(w)

	if m.maxReq > 0 && reqCount >= m.maxReq {
		oldest := w.entries[0].at
		retryAfter := m.windowDur - now.Sub(oldest)
		return Decision{Allowed: false, RetryAfter: retryAfter}
	}
	if m.maxTok > 0 && tokCount+tokenCost > m.maxTok {
		oldest := w.entries[0].at
		retryAfter := m.windowDur - now.Sub(oldest)
		return Decision{Allowed: false, RetryAfter: retryAfter}
	}

	remaining := 0
	if m.maxReq > 0 {
		remaining = m.maxReq - reqCount - 1
	}
	return Decision{Allowed: true, Remaining: remaining}
}

// Record adds the actual call to the window after it completes.
func (m *MemoryRateLimiter) Record(_ context.Context, id schema.Identity, tokensUsed int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := windowKey(id)
	now := time.Now()
	cutoff := now.Add(-m.windowDur)
	w := m.getOrCreate(key)
	purge(w, cutoff)
	w.entries = append(w.entries, windowEntry{at: now, tokens: tokensUsed})
}

func (m *MemoryRateLimiter) getOrCreate(key string) *slidingWindow {
	w, ok := m.windows[key]
	if !ok {
		w = &slidingWindow{}
		m.windows[key] = w
	}
	return w
}

func windowKey(id schema.Identity) string {
	return fmt.Sprintf("%s:%s:%s", id.TenantID, id.UserID, id.UseCaseID)
}

func purge(w *slidingWindow, cutoff time.Time) {
	i := 0
	for i < len(w.entries) && w.entries[i].at.Before(cutoff) {
		i++
	}
	w.entries = w.entries[i:]
}

func sumTokens(w *slidingWindow) int {
	n := 0
	for _, e := range w.entries {
		n += e.tokens
	}
	return n
}
