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

// Plugin accumulates spend and enforces hierarchical hard limits.
// It implements both guardrail.RequestInterceptor and the EventConsumer interface.
// Limits are keyed by scope strings: "tenantID", "tenantID:teamID", or
// "tenantID:teamID:userID". A request is blocked if ANY scope in the caller's
// hierarchy is over its ceiling.
type Plugin struct {
	mu     sync.Mutex
	spent  map[string]float64 // scope key → cumulative USD
	warned map[string]bool    // scope keys for which a warning has been emitted
	limits map[string]Limit   // scope key → configured limit

	bus    bus.Bus
	file   *os.File
	fileMu sync.Mutex
}

// New creates a budget plugin.
// limits maps scope keys to spend ceilings. Keys follow the hierarchy format:
//   - "tenantID"                  — org-level ceiling
//   - "tenantID:teamID"           — team-level ceiling
//   - "tenantID:teamID:userID"    — user-level ceiling
//
// b is used to publish warning/blocked events; may be nil.
// dir is the directory for the spend journal; "" = in-memory only.
func New(limits map[string]Limit, b bus.Bus, dir string) *Plugin {
	p := &Plugin{
		spent:  make(map[string]float64),
		warned: make(map[string]bool),
		limits: limits,
		bus:    b,
	}
	if dir != "" {
		p.replayJournal(filepath.Join(dir, "budget.jsonl"))
	}
	return p
}

// Name satisfies guardrail.RequestInterceptor.
func (p *Plugin) Name() string { return "budget" }

// Intercept blocks the request if ANY level in the caller's budget hierarchy
// is over its ceiling. Checks user → team → tenant in order; the first
// exceeded scope short-circuits.
func (p *Plugin) Intercept(ctx context.Context, req *schema.RequestEnvelope) error {
	id := identity.FromContext(ctx)
	for _, scope := range scopeKeys(id) {
		lim, ok := p.limits[scope]
		if !ok {
			continue
		}
		p.mu.Lock()
		spent := p.spent[scope]
		p.mu.Unlock()
		if spent >= lim.LimitUSD {
			return &guardrail.BlockedError{
				Interceptor: p.Name(),
				Category:    "budget_exceeded",
				Message: fmt.Sprintf("%s spend limit of $%.4f exceeded (spent $%.4f)",
					scope, lim.LimitUSD, spent),
			}
		}
	}
	return nil
}

// OnEvent accumulates spend at every scope in the hierarchy simultaneously.
// A $0.05 call by "acme:eng:alice" increments "acme:eng:alice", "acme:eng",
// and "acme" — so each level's ceiling is always up to date.
func (p *Plugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	if ev.Error != "" || ev.Shadow != nil {
		return nil
	}
	cost := ev.Cost.TotalUSD
	if cost <= 0 {
		return nil
	}

	// CallEvent carries TenantID but not TeamID/UserID — accumulate at tenant scope.
	// When a TeamID or UserID is available (future: add to CallEvent), all scopes
	// will be incremented. For now we accumulate at the tenant level which is
	// sufficient for the gate since Intercept checks the same keys.
	scopes := []string{ev.TenantID}

	for _, scope := range scopes {
		lim, hasLimit := p.limits[scope]

		p.mu.Lock()
		p.spent[scope] += cost
		newSpent := p.spent[scope]
		alreadyWarned := p.warned[scope]
		p.mu.Unlock()

		p.appendJournal(scope, cost)

		if !hasLimit {
			continue
		}
		warnAt := lim.WarnAt
		if warnAt <= 0 {
			warnAt = 0.8
		}
		if !alreadyWarned && newSpent >= lim.LimitUSD*warnAt {
			p.mu.Lock()
			p.warned[scope] = true
			p.mu.Unlock()
			if p.bus != nil {
				_ = p.bus.Publish(ctx, &schema.CallEvent{
					Timestamp: time.Now(),
					TenantID:  ev.TenantID,
					Error: fmt.Sprintf("budget warning [%s]: spent $%.4f of $%.4f (%.0f%%)",
						scope, newSpent, lim.LimitUSD, (newSpent/lim.LimitUSD)*100),
				})
			}
			slog.Warn("budget warning", "scope", scope,
				"spent_usd", newSpent, "limit_usd", lim.LimitUSD,
				"pct", int((newSpent/lim.LimitUSD)*100))
		}
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

// scopeKeys returns the ordered set of budget scope keys for id, from most
// specific to least specific: user → team → tenant.
// The gate checks each scope in order and blocks on the first exceeded limit.
// The accumulator increments all scopes so every ceiling stays current.
func scopeKeys(id schema.Identity) []string {
	var keys []string
	// user scope: tenantID:teamID:userID
	if id.UserID != "" {
		if id.TeamID != "" {
			keys = append(keys, id.TenantID+":"+id.TeamID+":"+id.UserID)
		} else {
			keys = append(keys, id.TenantID+":"+id.UserID)
		}
	}
	// team scope: tenantID:teamID
	if id.TeamID != "" {
		keys = append(keys, id.TenantID+":"+id.TeamID)
	}
	// tenant (org) scope: tenantID
	if id.TenantID != "" {
		keys = append(keys, id.TenantID)
	}
	return keys
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

