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
// Context-length errors escalate to the cheapest fallback with a larger context window.
// Context cancellation aborts immediately.
func Execute(ctx context.Context, decision RoutingDecision, req *schema.RequestEnvelope, lookup ProviderLookup, cfg RetryConfig) (ExecuteResult, error) {
	ordered := orderedCandidates(decision)
	var lastErr error
	totalAttempts := 0

	for i := 0; i < len(ordered); i++ {
		candidate := ordered[i]
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
				FallbackUsed: i > 0,
				FallbackFrom: decision.Model,
				Attempts:     totalAttempts,
			}, nil
		}

		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExecuteResult{Attempts: totalAttempts}, err
		}

		// Context-length overflow: rebuild the remaining candidates keeping only
		// those with a larger context window, sorted cheapest-first so we
		// escalate to the minimum viable model automatically.
		if IsContextLengthError(err) {
			currentWindow := ContextWindowFor(candidate.Model)
			ordered = escalateCandidates(ordered[i+1:], currentWindow)
			i = -1 // reset; i++ will make it 0
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

	for i := 0; i < len(ordered); i++ {
		candidate := ordered[i]
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
				FallbackUsed: i > 0,
				FallbackFrom: decision.Model,
				Attempts:     totalAttempts,
			}, nil
		}

		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExecuteResult{Attempts: totalAttempts}, err
		}

		if IsContextLengthError(err) {
			currentWindow := ContextWindowFor(candidate.Model)
			ordered = escalateCandidates(ordered[i+1:], currentWindow)
			i = -1
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

// streamWithRetry retries a failed stream only if it failed before emitting
// any chunk to the caller. Once fn has been called even once, the caller
// (typically an HTTP response writer) has already sent bytes downstream —
// retrying at that point would replay the response from the beginning and
// duplicate/corrupt whatever the client already received. So a failure
// after partial output ends the attempt loop immediately instead of
// retrying, same as a context cancellation.
func streamWithRetry(ctx context.Context, p provider.Provider, req *schema.RequestEnvelope, cfg RetryConfig, fn func(provider.StreamChunk) error) (int, error) {
	backoff := cfg.InitialBackoff
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		wrote := false
		err := p.Stream(ctx, req, func(chunk provider.StreamChunk) error {
			wrote = true
			return fn(chunk)
		})
		if err == nil {
			return i + 1, nil
		}
		lastErr = err
		if wrote || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isClientError(err) {
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

// escalateCandidates filters remaining candidates to those whose context window
// exceeds minWindow, then sorts them by ascending context window size so the
// smallest viable model (cheapest escalation) is tried first.
func escalateCandidates(remaining []Candidate, minWindow int) []Candidate {
	var eligible []Candidate
	for _, c := range remaining {
		if ContextWindowFor(c.Model) > minWindow {
			eligible = append(eligible, c)
		}
	}
	// Sort by ascending context window — smallest sufficient window wins.
	// This avoids jumping straight to a 1M-token model when a 200k model suffices.
	for i := 1; i < len(eligible); i++ {
		for j := i; j > 0 && ContextWindowFor(eligible[j].Model) < ContextWindowFor(eligible[j-1].Model); j-- {
			eligible[j], eligible[j-1] = eligible[j-1], eligible[j]
		}
	}
	return eligible
}
