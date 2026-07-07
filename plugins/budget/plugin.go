// Package budget provides spend tracking and enforcement for ixr.
// It implements both EventConsumer (accumulates spend post-call) and
// RequestInterceptor (gates requests pre-call when budget is exhausted).
//
// Limits are configured per identity key (tenantID:userID or tenantID alone).
// When spend reaches the warn threshold a warning CallEvent is published to the
// bus. When spend reaches the hard limit the request is blocked with a 429.
package budget

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/pkg/bus"
	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Limit defines a spend ceiling for one identity scope.
type Limit struct {
	// LimitUSD is the hard cap. Requests are blocked once this is reached.
	LimitUSD float64
	// WarnAt is the fraction (0–1) of LimitUSD at which to emit a warning event.
	// 0 disables warnings. Default: 0.8
	WarnAt float64
}

// deltaEntry is one line in the spend journal.
type deltaEntry struct {
	Key       string    `json:"key"`
	SpentUSD  float64   `json:"spent_usd"`
	Timestamp time.Time `json:"ts"`
}

// Plugin accumulates spend and enforces hard limits.
// It implements both guardrail.RequestInterceptor and the EventConsumer interface.
type Plugin struct {
	mu       sync.Mutex
	spent    map[string]float64  // identity key → cumulative USD
	warned   map[string]bool     // keys for which a warning has been emitted
	limits   map[string]Limit    // identity key → configured limit
	defaultL *Limit              // applied when no specific limit is found

	bus    bus.Bus
	file   *os.File
	fileMu sync.Mutex
}

// New creates a BudgetPlugin.
// limits maps identity keys (e.g. "acme:alice", "acme") to spend ceilings.
// defaultLimit applies to any identity not in the map; nil means no default cap.
// b is used to publish warning/blocked events; may be nil.
// dir is the directory for the spend journal; "" = in-memory only.
func New(limits map[string]Limit, defaultLimit *Limit, b bus.Bus, dir string) *Plugin {
	p := &Plugin{
		spent:    make(map[string]float64),
		warned:   make(map[string]bool),
		limits:   limits,
		defaultL: defaultLimit,
		bus:      b,
	}
	if dir != "" {
		p.replayJournal(filepath.Join(dir, "budget.jsonl"))
	}
	return p
}

// Name satisfies guardrail.RequestInterceptor.
func (p *Plugin) Name() string { return "budget" }

// Intercept blocks the request if the caller's identity is over budget.
// Satisfies guardrail.RequestInterceptor.
func (p *Plugin) Intercept(ctx context.Context, req *schema.RequestEnvelope) error {
	id := identity.FromContext(ctx)
	key := identityKey(id)
	lim := p.limitFor(key)
	if lim == nil {
		return nil // no limit configured for this identity
	}

	// Resolve the effective spend key — may differ from the limit key when
	// using tenant-level fallback (e.g. limit on "acme", spend tracked as "acme").
	spendKey := key
	if _, ok := p.limits[key]; !ok {
		parts := splitKey(key)
		if len(parts) > 1 {
			spendKey = parts[0]
		}
	}

	p.mu.Lock()
	spent := p.spent[spendKey]
	p.mu.Unlock()

	if spent >= lim.LimitUSD {
		return &guardrail.BlockedError{
			Interceptor: p.Name(),
			Category:    "budget_exceeded",
			Message: fmt.Sprintf("spend limit of $%.4f exceeded (spent $%.4f)",
				lim.LimitUSD, spent),
		}
	}
	return nil
}

// Name is also used as the EventConsumer name.
func (p *Plugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	if ev.Error != "" || ev.Shadow != nil {
		return nil // don't count failed or shadow calls
	}
	cost := ev.Cost.TotalUSD
	if cost <= 0 {
		return nil
	}

	id := schema.Identity{TenantID: ev.TenantID, UseCaseID: ev.UseCaseID}
	key := identityKey(id)
	lim := p.limitFor(key)

	p.mu.Lock()
	p.spent[key] += cost
	newSpent := p.spent[key]
	alreadyWarned := p.warned[key]
	p.mu.Unlock()

	p.appendJournal(key, cost)

	if lim == nil {
		return nil
	}

	warnAt := lim.WarnAt
	if warnAt <= 0 {
		warnAt = 0.8
	}

	// Emit warning once when crossing the threshold.
	if !alreadyWarned && newSpent >= lim.LimitUSD*warnAt {
		p.mu.Lock()
		p.warned[key] = true
		p.mu.Unlock()

		if p.bus != nil {
			_ = p.bus.Publish(ctx, &schema.CallEvent{
				Timestamp: time.Now(),
				TenantID:  ev.TenantID,
				UseCaseID: ev.UseCaseID,
				Error: fmt.Sprintf("budget warning: spent $%.4f of $%.4f limit (%.0f%%)",
					newSpent, lim.LimitUSD, (newSpent/lim.LimitUSD)*100),
			})
		}
		slog.Warn("budget warning",
			"key", key,
			"spent_usd", newSpent,
			"limit_usd", lim.LimitUSD,
			"pct", int((newSpent/lim.LimitUSD)*100))
	}

	return nil
}

// Spent returns the current cumulative spend for a key (for monitoring).
func (p *Plugin) Spent(key string) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spent[key]
}

// Reset clears the accumulated spend for a key (for testing or manual resets).
func (p *Plugin) Reset(key string) {
	p.mu.Lock()
	delete(p.spent, key)
	delete(p.warned, key)
	p.mu.Unlock()
}

func (p *Plugin) limitFor(key string) *Limit {
	if l, ok := p.limits[key]; ok {
		return &l
	}
	// Fall back to tenant-only key if the full key didn't match.
	parts := splitKey(key)
	if len(parts) > 1 {
		if l, ok := p.limits[parts[0]]; ok {
			return &l
		}
	}
	return p.defaultL
}

// --- Persistence ---

func (p *Plugin) replayJournal(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var e deltaEntry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		p.spent[e.Key] += e.SpentUSD
	}
	slog.Info("budget: replayed spend journal", "path", path, "keys", len(p.spent))

	// Open for appending after replay.
	af, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("budget: journal unavailable, running in-memory", "err", err)
		return
	}
	p.file = af
}

func (p *Plugin) appendJournal(key string, spentUSD float64) {
	if p.file == nil {
		return
	}
	line, err := json.Marshal(deltaEntry{Key: key, SpentUSD: spentUSD, Timestamp: time.Now()})
	if err != nil {
		return
	}
	p.fileMu.Lock()
	_, _ = p.file.Write(append(line, '\n'))
	p.fileMu.Unlock()
}

// Close flushes and closes the journal file.
func (p *Plugin) Close() error {
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

// identityKey builds a stable lookup key from an identity.
// Format: "tenantID:userID" or "tenantID" when userID is empty.
func identityKey(id schema.Identity) string {
	if id.UserID != "" {
		return id.TenantID + ":" + id.UserID
	}
	return id.TenantID
}

func splitKey(key string) []string {
	for i, c := range key {
		if c == ':' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}
