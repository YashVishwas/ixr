package ixr

import (
	"context"
	"testing"

	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// stubProvider is a minimal provider.Provider for testing registry/router
// wiring — never actually called in these tests, only resolved.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Chat(context.Context, *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	return nil, nil
}
func (s *stubProvider) Stream(context.Context, *schema.RequestEnvelope, func(provider.StreamChunk) error) error {
	return nil
}

func TestAvailableCatalog_FiltersToConfiguredProviders(t *testing.T) {
	cat := []routing.ModelCard{
		{ID: "claude-opus-4.7"},      // needs anthropic
		{ID: "gpt-5.2"},              // needs openai
		{ID: "gpt-oss-120b"},         // needs cerebras
		{ID: "mistral-small-latest"}, // needs mistral
	}

	// Only cerebras and mistral configured — the demo-shaped deployment
	// this issue was written about (wayfinder's Playground configures
	// Cerebras, Groq, Mistral; none of the frontier-only entries above).
	registry := map[string]provider.Provider{
		"cerebras": &stubProvider{name: "cerebras"},
		"mistral":  &stubProvider{name: "mistral"},
	}
	router := buildRouter(registry)

	got := availableCatalog(cat, router)

	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids["gpt-oss-120b"] || !ids["mistral-small-latest"] {
		t.Errorf("expected only the provider-configured entries to survive, got %+v", got)
	}
	if ids["claude-opus-4.7"] || ids["gpt-5.2"] {
		t.Errorf("expected entries with no configured provider to be filtered out, got %+v", got)
	}
}

func TestAvailableCatalog_EmptyRegistry_ReturnsEmptyNotNilPanic(t *testing.T) {
	cat := []routing.ModelCard{{ID: "claude-opus-4.7"}, {ID: "gpt-5.2"}}
	router := buildRouter(map[string]provider.Provider{}) // nothing configured

	got := availableCatalog(cat, router)

	if len(got) != 0 {
		t.Fatalf("expected an empty catalog when no providers are configured, got %+v", got)
	}
}

func TestAvailableCatalog_AllConfigured_NothingDropped(t *testing.T) {
	cat := []routing.ModelCard{{ID: "claude-opus-4.7"}, {ID: "gpt-5.2"}}
	registry := map[string]provider.Provider{
		"anthropic": &stubProvider{name: "anthropic"},
		"openai":    &stubProvider{name: "openai"},
	}
	router := buildRouter(registry)

	got := availableCatalog(cat, router)

	if len(got) != len(cat) {
		t.Fatalf("expected nothing filtered when every provider is configured, got %d of %d", len(got), len(cat))
	}
}

func TestAvailableCatalog_PreservesOrder(t *testing.T) {
	cat := []routing.ModelCard{
		{ID: "gpt-5.2"},
		{ID: "gpt-oss-120b"},
		{ID: "gpt-5.3-codex"},
		{ID: "mistral-small-latest"},
	}
	registry := map[string]provider.Provider{
		"openai":   &stubProvider{name: "openai"},
		"cerebras": &stubProvider{name: "cerebras"},
		"mistral":  &stubProvider{name: "mistral"},
	}
	router := buildRouter(registry)

	got := availableCatalog(cat, router)

	want := []string{"gpt-5.2", "gpt-oss-120b", "gpt-5.3-codex", "mistral-small-latest"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d: got %q, want %q (order should be preserved)", i, got[i].ID, id)
		}
	}
}

// TestAvailableCatalog_DemoDeployment_MatchesRealCatalogEntries is an
// integration-shaped check against the real routing.Catalog(), not a
// synthetic one — proves the three demo-shaped entries added to the
// catalog actually resolve through buildRouter's real dispatch logic
// (prefix matching, not just a hypothetical scenario), the way wayfinder's
// Playground BFF (Cerebras + Groq + Mistral only) would see it.
func TestAvailableCatalog_DemoDeployment_MatchesRealCatalogEntries(t *testing.T) {
	registry := map[string]provider.Provider{
		"cerebras": &stubProvider{name: "cerebras"},
		"llama":    &stubProvider{name: "llama"}, // ixr's Groq-backed provider key
		"mistral":  &stubProvider{name: "mistral"},
	}
	router := buildRouter(registry)

	got := availableCatalog(routing.Catalog(), router)

	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	for _, want := range []string{"gpt-oss-120b", "llama-3.1-8b-instant", "mistral-small-latest"} {
		if !ids[want] {
			t.Errorf("expected %q to survive filtering against a Cerebras+Groq+Mistral-only registry, got %+v", want, got)
		}
	}
	// And nothing from the frontier-only entries should have snuck through.
	if ids["claude-opus-4.7"] || ids["gpt-5.2"] || ids["gemini-3.1-pro"] {
		t.Errorf("expected frontier entries with no configured provider to be filtered out, got %+v", got)
	}
}
