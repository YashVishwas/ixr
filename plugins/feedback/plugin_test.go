package feedback

import (
	"context"
	"testing"
	"time"

	domainfeedback "github.com/YashVishwas/ixr/internal/domain/feedback"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestPlugin_IndexesNonShadowEvent(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	p := New(store)

	err := p.OnEvent(context.Background(), &schema.CallEvent{ID: "chatcmpl-1", Model: "gpt-4o", AutoRouted: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := store.Lookup("chatcmpl-1")
	if !ok {
		t.Fatal("expected the call to be indexed")
	}
	if got.Model != "gpt-4o" || !got.AutoRouted {
		t.Errorf("got %+v, want {Model: gpt-4o, AutoRouted: true}", got)
	}
}

func TestPlugin_SkipsShadowEvents(t *testing.T) {
	store := domainfeedback.NewStore(0, time.Minute)
	p := New(store)

	err := p.OnEvent(context.Background(), &schema.CallEvent{
		ID:     "shadow-1",
		Model:  "gpt-4o",
		Shadow: &schema.ShadowMetadata{PrimaryID: "chatcmpl-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := store.Lookup("shadow-1"); ok {
		t.Fatal("expected a shadow event not to be indexed — its ID is never visible to a caller")
	}
}

func TestPlugin_IndexesExplicitModelRequestsToo(t *testing.T) {
	// Unlike plugins/banditreward (which ignores non-auto-routed calls
	// since the bandit shouldn't train on them), indexing itself doesn't
	// discriminate — AutoRouted is recorded so the feedback handler can
	// make that decision later, at the point it actually matters (whether
	// to call bandit.Update).
	store := domainfeedback.NewStore(0, time.Minute)
	p := New(store)

	err := p.OnEvent(context.Background(), &schema.CallEvent{ID: "chatcmpl-2", Model: "gpt-4o", AutoRouted: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := store.Lookup("chatcmpl-2")
	if !ok {
		t.Fatal("expected the call to be indexed even though it wasn't auto-routed")
	}
	if got.AutoRouted {
		t.Error("expected AutoRouted=false to be preserved")
	}
}
