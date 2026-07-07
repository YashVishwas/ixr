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

func makeEvent(tenantID, userID string, costUSD float64) *schema.CallEvent {
	return &schema.CallEvent{
		Timestamp: time.Now(),
		TenantID:  tenantID,
		Request:   schema.RequestEnvelope{Model: "gpt-4o"},
		Cost:      schema.CostBreakdown{TotalUSD: costUSD},
	}
}

func ctxWithIdentity(tenantID, userID string) context.Context {
	return identity.WithIdentity(context.Background(), schema.Identity{
		TenantID: tenantID,
		UserID:   userID,
	})
}

// --- Accumulation ---

func TestAccumulate_SingleCall(t *testing.T) {
	p := New(nil, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 0.05))
	if got := p.Spent("acme"); got != 0.05 {
		t.Fatalf("expected 0.05, got %f", got)
	}
}

func TestAccumulate_MultipleCallsSameKey(t *testing.T) {
	p := New(nil, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 0.10))
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 0.15))
	if got := p.Spent("acme"); got != 0.25 {
		t.Fatalf("expected 0.25, got %f", got)
	}
}

func TestAccumulate_FailedCallsNotCounted(t *testing.T) {
	p := New(nil, nil, nil, "")
	ev := makeEvent("acme", "", 0.10)
	ev.Error = "provider error"
	_ = p.OnEvent(context.Background(), ev)
	if got := p.Spent("acme"); got != 0 {
		t.Fatalf("failed call should not count toward spend, got %f", got)
	}
}

func TestAccumulate_ShadowCallsNotCounted(t *testing.T) {
	p := New(nil, nil, nil, "")
	ev := makeEvent("acme", "", 0.10)
	ev.Shadow = &schema.ShadowMetadata{PrimaryModel: "gpt-4o", ShadowModel: "claude"}
	_ = p.OnEvent(context.Background(), ev)
	if got := p.Spent("acme"); got != 0 {
		t.Fatalf("shadow call should not count toward spend, got %f", got)
	}
}

// --- Gate ---

func TestIntercept_UnderLimit(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 10.0, WarnAt: 0.8}}
	p := New(limits, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 5.0))

	ctx := ctxWithIdentity("acme", "")
	if err := p.Intercept(ctx, &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("expected no block under limit, got: %v", err)
	}
}

func TestIntercept_AtLimit(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 10.0, WarnAt: 0.8}}
	p := New(limits, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 10.0))

	ctx := ctxWithIdentity("acme", "")
	err := p.Intercept(ctx, &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block at limit")
	}
	if blocked, ok := err.(*guardrail.BlockedError); !ok || blocked.Category != "budget_exceeded" {
		t.Fatalf("expected budget_exceeded BlockedError, got: %v", err)
	}
}

func TestIntercept_NoLimitConfigured(t *testing.T) {
	p := New(nil, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 999.0))
	ctx := ctxWithIdentity("acme", "")
	if err := p.Intercept(ctx, &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("no limit configured — should never block, got: %v", err)
	}
}

func TestIntercept_TenantFallback(t *testing.T) {
	// Limit set for "acme" (tenant-only). Spend accumulates under "acme"
	// since CallEvent has no UserID. Gate checks "acme:alice" first, then
	// falls back to "acme" limit — should block.
	limits := map[string]Limit{"acme": {LimitUSD: 1.0}}
	p := New(limits, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 1.0)) // accumulates as "acme"

	ctx := ctxWithIdentity("acme", "alice") // gate key is "acme:alice" → fallback to "acme"
	err := p.Intercept(ctx, &schema.RequestEnvelope{})
	if err == nil {
		t.Fatal("expected block via tenant fallback limit")
	}
}

// --- Warning ---

func TestWarn_EmittedOnceAtThreshold(t *testing.T) {
	var published int
	fakeBus := &countBus{&published}
	limits := map[string]Limit{"acme": {LimitUSD: 10.0, WarnAt: 0.8}}
	p := New(limits, nil, fakeBus, "")

	// Spend 8.0 (80% of 10) — should emit warning.
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 8.0))
	if published != 1 {
		t.Fatalf("expected 1 warning event, got %d", published)
	}

	// Additional spend — warning should NOT be re-emitted.
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 0.5))
	if published != 1 {
		t.Fatalf("warning should only emit once, got %d", published)
	}
}

// --- Reset ---

func TestReset(t *testing.T) {
	limits := map[string]Limit{"acme": {LimitUSD: 1.0}}
	p := New(limits, nil, nil, "")
	_ = p.OnEvent(context.Background(), makeEvent("acme", "", 1.0))

	p.Reset("acme")
	ctx := ctxWithIdentity("acme", "")
	if err := p.Intercept(ctx, &schema.RequestEnvelope{}); err != nil {
		t.Fatalf("after reset should not block, got: %v", err)
	}
}

// --- identityKey ---

func TestIdentityKey_WithUser(t *testing.T) {
	k := identityKey(schema.Identity{TenantID: "acme", UserID: "alice"})
	if k != "acme:alice" {
		t.Fatalf("expected acme:alice, got %q", k)
	}
}

func TestIdentityKey_TenantOnly(t *testing.T) {
	k := identityKey(schema.Identity{TenantID: "acme"})
	if k != "acme" {
		t.Fatalf("expected acme, got %q", k)
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
