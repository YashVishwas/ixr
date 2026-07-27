package chain

import "testing"

func TestBuildRegistry_Valid(t *testing.T) {
	reg, err := BuildRegistry(map[string]Def{
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
	if c.Strategy != StrategySequential {
		t.Errorf("strategy: got %q, want %q (default)", c.Strategy, StrategySequential)
	}
	if len(c.Models) != 2 || c.Models[1] != "model-b" {
		t.Errorf("chain: got %+v", c)
	}
}

func TestBuildRegistry_MismatchedLengths(t *testing.T) {
	_, err := BuildRegistry(map[string]Def{
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
	_, err := BuildRegistry(map[string]Def{
		"empty": {Models: nil, Prompts: nil},
	})
	if err == nil {
		t.Fatal("expected error for chain with no models")
	}
}

func TestBuildRegistry_FusionValid(t *testing.T) {
	reg, err := BuildRegistry(map[string]Def{
		"debate": {
			Strategy: "fusion",
			Models:   []string{"model-a", "model-b"},
			Judge:    "model-c",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := reg.Lookup("debate")
	if !ok {
		t.Fatal("expected chain to be found")
	}
	if c.Strategy != StrategyFusion {
		t.Errorf("strategy: got %q, want %q", c.Strategy, StrategyFusion)
	}
	if c.Judge != "model-c" {
		t.Errorf("judge: got %q, want %q", c.Judge, "model-c")
	}
}

func TestBuildRegistry_FusionRequiresJudge(t *testing.T) {
	_, err := BuildRegistry(map[string]Def{
		"debate": {
			Strategy: "fusion",
			Models:   []string{"model-a", "model-b"},
		},
	})
	if err == nil {
		t.Fatal("expected error for fusion chain with no judge")
	}
}

func TestBuildRegistry_FusionRequiresAtLeastTwoModels(t *testing.T) {
	_, err := BuildRegistry(map[string]Def{
		"debate": {
			Strategy: "fusion",
			Models:   []string{"model-a"},
			Judge:    "model-c",
		},
	})
	if err == nil {
		t.Fatal("expected error for fusion chain with fewer than 2 panel models")
	}
}

func TestBuildRegistry_UnknownStrategy(t *testing.T) {
	_, err := BuildRegistry(map[string]Def{
		"bad": {
			Strategy: "parallel-vote",
			Models:   []string{"model-a", "model-b"},
			Prompts:  []string{"", ""},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestRegistry_LookupMiss(t *testing.T) {
	reg := Registry{}
	if _, ok := reg.Lookup("not-a-chain"); ok {
		t.Error("expected lookup miss for unregistered name")
	}
}
