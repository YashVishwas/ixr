package routing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// ProviderLookup resolves a model name to a live Provider instance.
type ProviderLookup func(model string) (provider.Provider, error)

// RetryConfig controls exponential backoff for transient provider errors.
type RetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig is a sensible starting point for production use.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts:    3,
	InitialBackoff: 200 * time.Millisecond,
	MaxBackoff:     5 * time.Second,
	BackoffFactor:  2.0,
}

// ExecuteResult captures the outcome of Execute or ExecuteStream.
type ExecuteResult struct {
	Response     *schema.ResponseEnvelope
	Provider     provider.Provider
	Model        string
	FallbackUsed bool
	FallbackFrom string
	Attempts     int
}

// Execute calls the primary model with retry/backoff, then tries the fallback chain.
// 4xx errors skip retries and move immediately to the next fallback.
// Context cancellation aborts immediately.
func Execute(ctx context.Context, decision RoutingDecision, req *schema.RequestEnvelope, lookup ProviderLookup, cfg RetryConfig) (ExecuteResult, error) {
	ordered := orderedCandidates(decision)
	var lastErr error
	totalAttempts := 0

	for idx, candidate := range ordered {
		p, err := lookup(candidate.Model)
		if err != nil {
			lastErr = err
			continue
		}

		resp, attempts, err := chatWithRetry(ctx, p, req, cfg)
		totalAttempts += attempts

		if err == nil {
			return ExecuteResult{
				Response:     resp,
				Provider:     p,
				Model:        candidate.Model,
				FallbackUsed: idx > 0,
				FallbackFrom: decision.Model,
				Attempts:     totalAttempts,
			}, nil
		}

		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExecuteResult{Attempts: totalAttempts}, err
		}
	}

	return ExecuteResult{Attempts: totalAttempts}, lastErr
}

// ExecuteStream mirrors Execute but calls Provider.Stream instead of Chat.
// fn receives chunks from the winning provider; the first non-error provider wins.
func ExecuteStream(ctx context.Context, decision RoutingDecision, req *schema.RequestEnvelope, lookup ProviderLookup, cfg RetryConfig, fn func(provider.StreamChunk) error) (ExecuteResult, error) {
	ordered := orderedCandidates(decision)
	var lastErr error
	totalAttempts := 0

	for idx, candidate := range ordered {
		p, err := lookup(candidate.Model)
		if err != nil {
			lastErr = err
			continue
		}

		var attempts int
		attempts, err = streamWithRetry(ctx, p, req, cfg, fn)
		totalAttempts += attempts

		if err == nil {
			return ExecuteResult{
				Provider:     p,
				Model:        candidate.Model,
				FallbackUsed: idx > 0,
				FallbackFrom: decision.Model,
				Attempts:     totalAttempts,
			}, nil
		}

		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExecuteResult{Attempts: totalAttempts}, err
		}
	}

	return ExecuteResult{Attempts: totalAttempts}, lastErr
}

func chatWithRetry(ctx context.Context, p provider.Provider, req *schema.RequestEnvelope, cfg RetryConfig) (*schema.ResponseEnvelope, int, error) {
	backoff := cfg.InitialBackoff
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		resp, err := p.Chat(ctx, req)
		if err == nil {
			return resp, i + 1, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isClientError(err) {
			return nil, i + 1, err
		}
		if i < cfg.MaxAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, i + 1, ctx.Err()
			case <-time.After(backoff):
			}
			backoff = minDuration(time.Duration(float64(backoff)*cfg.BackoffFactor), cfg.MaxBackoff)
		}
	}
	return nil, cfg.MaxAttempts, lastErr
}

func streamWithRetry(ctx context.Context, p provider.Provider, req *schema.RequestEnvelope, cfg RetryConfig, fn func(provider.StreamChunk) error) (int, error) {
	backoff := cfg.InitialBackoff
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		err := p.Stream(ctx, req, fn)
		if err == nil {
			return i + 1, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isClientError(err) {
			return i + 1, err
		}
		if i < cfg.MaxAttempts-1 {
			select {
			case <-ctx.Done():
				return i + 1, ctx.Err()
			case <-time.After(backoff):
			}
			backoff = minDuration(time.Duration(float64(backoff)*cfg.BackoffFactor), cfg.MaxBackoff)
		}
	}
	return cfg.MaxAttempts, lastErr
}

// isClientError detects HTTP 4xx responses by inspecting the error message.
// All current provider adapters format errors as "name: status 4XX: body".
func isClientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, pat := range []string{"status 400", "status 401", "status 403", "status 404", "status 422", "status 429", ": 400 ", ": 401 ", ": 403 ", ": 404 "} {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	return false
}

func orderedCandidates(d RoutingDecision) []Candidate {
	out := make([]Candidate, 0, 1+len(d.FallbackChain))
	out = append(out, Candidate{Model: d.Model})
	out = append(out, d.FallbackChain...)
	return out
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
