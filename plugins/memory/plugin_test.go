package memory

import (
	"context"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/memory"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeCallEvent(tenantID, userID, userMessage string) *schema.CallEvent {
	return &schema.CallEvent{
		TenantID: tenantID,
		UserID:   userID,
		Request: schema.RequestEnvelope{
			Messages: []schema.Message{{Role: "user", Content: userMessage}},
		},
		Response: schema.ResponseEnvelope{
			Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "ok"}}},
		},
	}
}

func TestUserKeyFromEvent_RequiresUserID(t *testing.T) {
	if got := userKeyFromEvent(&schema.CallEvent{TenantID: "acme"}); got != "" {
		t.Fatalf("expected empty key without UserID, got %q", got)
	}
	if got := userKeyFromEvent(&schema.CallEvent{TenantID: "acme", UserID: "alice"}); got != "acme:alice" {
		t.Fatalf("got %q, want acme:alice", got)
	}
}

func TestOnEvent_RequiresUserID(t *testing.T) {
	store := memory.NewMemoryStore("")
	p := New(store, memory.RuleExtractor{})

	ev := makeCallEvent("acme", "", "My name is Arun.")
	if err := p.OnEvent(context.Background(), ev); err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	entries, _ := store.All(context.Background(), "acme")
	if len(entries) != 0 {
		t.Fatalf("expected no memories stored without a UserID, got %+v", entries)
	}
}

func TestOnEvent_IsolatesUsersWithinSameTenant(t *testing.T) {
	store := memory.NewMemoryStore("")
	p := New(store, memory.RuleExtractor{})

	_ = p.OnEvent(context.Background(), makeCallEvent("acme", "alice", "My name is Alice."))
	_ = p.OnEvent(context.Background(), makeCallEvent("acme", "bob", "My name is Bob."))

	aliceEntries, _ := store.Recent(context.Background(), "acme:alice", 10)
	bobEntries, _ := store.Recent(context.Background(), "acme:bob", 10)

	if len(aliceEntries) != 1 || aliceEntries[0].Content != "User's name is Alice" {
		t.Fatalf("alice entries: %+v", aliceEntries)
	}
	if len(bobEntries) != 1 || bobEntries[0].Content != "User's name is Bob" {
		t.Fatalf("bob entries: %+v", bobEntries)
	}
}
