package routing

import "testing"

func TestLookup_CatalogTakesPrecedenceOverPricingTable(t *testing.T) {
	// claude-sonnet-4-6 is in the curated auto-routing catalog; confirm
	// Lookup returns the full ModelCard (capability priors included), not
	// just a pricing-only stub.
	card, ok := Lookup("claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected claude-sonnet-4-6 to be found")
	}
	if card.Reasoning == 0 {
		t.Error("expected catalog entry with capability priors, got a pricing-only stub")
	}
}

func TestLookup_FallsBackToPricingTable(t *testing.T) {
	// llama-3.3-70b-versatile is a real, commonly-configured model (see
	// demo-ixr.yaml) that isn't an auto-routing candidate — this is exactly
	// the case that silently priced at $0 before pricingTable existed.
	card, ok := Lookup("llama-3.3-70b-versatile")
	if !ok {
		t.Fatal("expected llama-3.3-70b-versatile to be found via pricingTable fallback")
	}
	if card.InputUSDPer1M != 0.59 || card.OutputUSDPer1M != 0.79 {
		t.Errorf("pricing: got %+v, want {0.59 0.79}", card)
	}
}

func TestLookup_UnknownModelNotFound(t *testing.T) {
	if _, ok := Lookup("totally-made-up-model-xyz"); ok {
		t.Error("expected unknown model to return ok=false, not a zero-value match")
	}
}

func TestPricingTable_NoZeroRates(t *testing.T) {
	// A {0,0} entry would make cost.ForUsage silently treat a genuinely
	// priced model as free — the same bug this table exists to fix.
	for model, p := range pricingTable {
		if p.InputUSDPer1M <= 0 || p.OutputUSDPer1M <= 0 {
			t.Errorf("%s: non-positive rate %+v", model, p)
		}
	}
}
