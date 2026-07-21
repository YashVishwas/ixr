package chain

import "testing"

func TestBuildRegistry_Valid(t *testing.T) {
	reg, err := BuildRegistry(map[string]struct {
		Models  []string
		Prompts []string
	}{
		"fast-refine": {
			Models:  []string{"model-a", "model-b"},
			Prompts: []string{"", "refine it"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := reg.Lookup("fast-refine")
	if !ok {
		t.Fatal("expected chain to be found")
	}
	if len(c.Models) != 2 || c.Models[1] != "model-b" {
		t.Errorf("chain: got %+v", c)
	}
}

func TestBuildRegistry_MismatchedLengths(t *testing.T) {
	_, err := BuildRegistry(map[string]struct {
		Models  []string
		Prompts []string
	}{
		"bad": {
			Models:  []string{"model-a", "model-b"},
			Prompts: []string{""},
		},
	})
	if err == nil {
		t.Fatal("expected error for mismatched models/prompts length")
	}
}

func TestBuildRegistry_EmptyModels(t *testing.T) {
	_, err := BuildRegistry(map[string]struct {
		Models  []string
		Prompts []string
	}{
		"empty": {Models: nil, Prompts: nil},
	})
	if err == nil {
		t.Fatal("expected error for chain with no models")
	}
}

func TestRegistry_LookupMiss(t *testing.T) {
	reg := Registry{}
	if _, ok := reg.Lookup("not-a-chain"); ok {
		t.Error("expected lookup miss for unregistered name")
	}
}
