package budget

import (
	"context"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/plugin"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeEvent(tenantID string, costUSD float64) *schema.CallEvent {
	return &schema.CallEvent{
		Timestamp: time.Now(),
		TenantID:  tenantID,
		Request:   schema.RequestEnvelope{Model: "gpt-4o"},
		Cost:      schema.CostBreakdown{TotalUSD: costUSD},
	}
}

func makeEventFull(tenantID, teamID, userID string, costUSD float64) *schema.CallEvent {
	ev := makeEvent(tenantID, costUSD)
	ev.TeamID = teamID
	ev.UserID = userID
	return ev
}

func ctxWith(tenantID, teamID, userID string) context.Context {
	return identity.WithIdentity(context.Background(), schema.Identity{
		TenantID: tenantID,
		TeamID:   teamID,
		UserID:   userID,
	})
}

// --- Accumulation ---

func TestAccumulate_SingleCall(t *testing.T) {
	p := New(nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 0.05))
	if got := p.Spent("acme"); got != 0.05 {
		t.Fatalf("expected 0.05, got %f", got)
	}
}

func TestAccumulate_MultipleCalls(t *testing.T) {
	p := New(nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 0.10))
	_ = p.OnEvent(context.Background(), makeEvent("acme", 0.15))
	if got := p.Spent("acme"); got != 0.25 {
		t.Fatalf("expected 0.25, got %f", got)
	}
}

func TestAccumulate_FailedCallsNotCounted(t *testing.T) {
	p := New(nil, nil, "")
	ev := makeEvent("acme", 0.10)
	ev.Error = "provider error"
	_ = p.OnEvent(context.Background(), ev)
	if got := p.Spent("acme"); got != 0 {
		t.Fatalf("failed call should not count, got %f", got)
	}
}

func TestAccumulate_ShadowCallsNotCounted(t *testing.T) {
	p := New(nil, nil, "")
	ev := makeEvent("acme", 0.10)
	ev.Shadow = &schema.ShadowMetadata{PrimaryModel: "gpt-4o", ShadowModel: "claude"}
	_ = p.OnEvent(context.Background(), ev)
	if got := p.Spent("acme"); got != 0 {
		t.Fatalf("shadow call should not count, got %f", got)
	}
}

func TestAccumulate_SingleCallUpdatesAllHierarchyScopes(t *testing.T) {
	// A single event from a user in a team should bump the user, team, and
	// tenant scopes together — the point of "hierarchical" enforcement.
	p := New(nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEventFull("acme", "eng", "alice", 0.05))

	if got := p.Spent("acme:eng:alice"); got != 0.05 {
		t.Errorf("user scope: got %f, want 0.05", got)
	}
	if got := p.Spent("acme:eng"); got != 0.05 {
		t.Errorf("team scope: got %f, want 0.05", got)
	}
	if got := p.Spent("acme"); got != 0.05 {
		t.Errorf("tenant scope: got %f, want 0.05", got)
	}
}

func TestAccumulate_RealEventBlocksSubsequentTeamRequest(t *testing.T) {
	// End-to-end: accumulate from real CallEvents (not manual p.spent pokes),
	// then confirm Intercept actually blocks once the team ceiling is hit.
	limits := map[string]Limit{"acme:eng": {LimitUSD: 0.05}}
	p := New(limits, nil, "")
	_ = p.OnEvent(context.Background(), makeEventFull("acme", "eng", "alice", 0.05))

	err := p.Intercept(ctxWith("acme", "eng", "alice"), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block: team spend reached from real event accumulation")
	}
}

// --- Gate ---

func TestIntercept_UnderLimit(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 10.0}}
	p := New(limits, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 5.0))
	if err := p.Intercept(ctxWith("acme", "", ""), &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("expected no block, got: %v", err)
	}
}

func TestIntercept_AtLimit(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 10.0}}
	p := New(limits, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 10.0))

	err := p.Intercept(ctxWith("acme", "", ""), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block at limit")
	}
	if blocked, ok := err.(*guardrail.BlockedError); !ok || blocked.Category != "budget_exceeded" {
		t.Fatalf("expected budget_exceeded, got: %v", err)
	}
}

func TestIntercept_NoLimitConfigured(t *testing.T) {
	p := New(nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 999.0))
	if err := p.Intercept(ctxWith("acme", "", ""), &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("no limit — should never block, got: %v", err)
	}
}

// --- Hierarchy ---

func TestHierarchy_TenantLimitBlocksUserRequest(t *testing.T) {
	// Org ceiling breached — individual user should be blocked.
	limits := map[string]Limit{"acme": {LimitUSD: 1.0}}
	p := New(limits, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 1.0)) // hits org ceiling

	// User request walks: user → tenant → blocked at tenant level.
	err := p.Intercept(ctxWith("acme", "", "alice"), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block: org limit exceeded")
	}
}

func TestHierarchy_TeamLimitBlocksUser(t *testing.T) {
	// Team ceiling breached — user in that team is blocked even if org is fine.
	limits := map[string]Limit{
		"acme":     {LimitUSD: 100.0}, // org: plenty of headroom
		"acme:eng": {LimitUSD: 5.0},   // eng team: tight
	}
	p := New(limits, nil, "")
	p.mu.Lock()
	p.spent["acme:eng"] = 5.0 // directly set team spend at ceiling
	p.mu.Unlock()

	// Engineer alice is in the eng team — blocked at team level.
	err := p.Intercept(ctxWith("acme", "eng", "alice"), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block: team limit exceeded")
	}
}

func TestHierarchy_UserUnderLimitTeamOk(t *testing.T) {
	limits := map[string]Limit{
		"acme":     {LimitUSD: 100.0},
		"acme:eng": {LimitUSD: 50.0},
	}
	p := New(limits, nil, "")
	p.mu.Lock()
	p.spent["acme"] = 10.0
	p.spent["acme:eng"] = 5.0
	p.mu.Unlock()

	if err := p.Intercept(ctxWith("acme", "eng", "alice"), &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("expected pass — all scopes under limit, got: %v", err)
	}
}

func TestHierarchy_OrgLimitBlocksEvenIfTeamUnder(t *testing.T) {
	limits := map[string]Limit{
		"acme":     {LimitUSD: 10.0},
		"acme:eng": {LimitUSD: 50.0}, // team limit is generous
	}
	p := New(limits, nil, "")
	p.mu.Lock()
	p.spent["acme"] = 10.0 // org at ceiling
	p.spent["acme:eng"] = 3.0
	p.mu.Unlock()

	err := p.Intercept(ctxWith("acme", "eng", "alice"), &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block: org limit exceeded despite team being under")
	}
}

// --- scopeKeys ---

func TestScopeKeys_UserTeamTenant(t *testing.T) {
	id := schema.Identity{TenantID: "acme", TeamID: "eng", UserID: "alice"}
	keys := scopeKeys(id)
	if len(keys) != 3 {
		t.Fatalf("expected 3 scope keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "acme:eng:alice" {
		t.Errorf("keys[0] = %q, want acme:eng:alice", keys[0])
	}
	if keys[1] != "acme:eng" {
		t.Errorf("keys[1] = %q, want acme:eng", keys[1])
	}
	if keys[2] != "acme" {
		t.Errorf("keys[2] = %q, want acme", keys[2])
	}
}

func TestScopeKeys_TenantOnly(t *testing.T) {
	keys := scopeKeys(schema.Identity{TenantID: "acme"})
	if len(keys) != 1 || keys[0] != "acme" {
		t.Fatalf("expected [acme], got %v", keys)
	}
}

func TestScopeKeys_UserNoTeam(t *testing.T) {
	keys := scopeKeys(schema.Identity{TenantID: "acme", UserID: "alice"})
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %v", keys)
	}
	if keys[0] != "acme:alice" || keys[1] != "acme" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

// --- Warning ---

func TestWarn_EmittedOnceAtThreshold(t *testing.T) {
	var published int
	limits := map[string]Limit{"acme": {LimitUSD: 10.0, WarnAt: 0.8}}
	p := New(limits, &countBus{&published}, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 8.0))
	if published != 1 {
		t.Fatalf("expected 1 warning, got %d", published)
	}
	_ = p.OnEvent(context.Background(), makeEvent("acme", 0.5))
	if published != 1 {
		t.Fatalf("warning should emit once only, got %d", published)
	}
}

// --- Reset ---

func TestReset(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 1.0}}
	p := New(limits, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", 1.0))
	p.Reset("acme")
	if err := p.Intercept(ctxWith("acme", "", ""), &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("after reset should not block, got: %v", err)
	}
}

// --- helpers ---

type countBus struct{ n *int }

func (b *countBus) Publish(_ context.Context, _ *schema.CallEvent) error {
	*b.n++
	return nil
}
func (b *countBus) Subscribe(_ plugin.EventConsumer) {}
func (b *countBus) Start(_ context.Context)          {}
