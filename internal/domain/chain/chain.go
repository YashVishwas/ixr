// Package chain implements named model chains (RFC Gap 11): a request names
// a chain instead of a single model, and ixr runs either a fixed sequence
// of models (each step's output feeding the next step's prompt) or a fusion
// panel (models run in parallel, then a judge model synthesizes the final
// answer) — the closest equivalent to OmniRoute's sequential-pipeline and
// parallel-panel-plus-judge routing strategies.
package chain

import "fmt"

// Strategy selects how a chain's models are run.
type Strategy string

const (
	// StrategySequential runs Models in order, each step's reply feeding
	// the next step's prompt. This is the default when unset.
	StrategySequential Strategy = "sequential"
	// StrategyFusion runs Models in parallel against the caller's original
	// messages, then routes their combined answers to Judge for synthesis.
	StrategyFusion Strategy = "fusion"
)

// Chain is a named chain of model calls.
type Chain struct {
	Name     string
	Strategy Strategy
	Models   []string // sequential: ordered steps. fusion: panel members.
	Prompts  []string // sequential only; same length as Models
	Judge    string   // fusion only: model that synthesizes the panel's answers
}

// Registry maps chain name -> Chain.
type Registry map[string]Chain

// Lookup returns the chain for name, or false if name isn't a registered chain.
func (r Registry) Lookup(name string) (Chain, bool) {
	c, ok := r[name]
	return c, ok
}

// Def is the raw, pre-validation definition of a chain, as loaded from config.
type Def struct {
	Strategy string
	Models   []string
	Prompts  []string
	Judge    string
}

// BuildRegistry validates and constructs a Registry from raw definitions.
func BuildRegistry(defs map[string]Def) (Registry, error) {
	reg := make(Registry, len(defs))
	for name, def := range defs {
		strategy := Strategy(def.Strategy)
		if strategy == "" {
			strategy = StrategySequential
		}

		switch strategy {
		case StrategySequential:
			if len(def.Models) == 0 {
				return nil, fmt.Errorf("chain %q: must define at least one model", name)
			}
			if len(def.Prompts) != len(def.Models) {
				return nil, fmt.Errorf("chain %q: models (%d) and prompts (%d) must be the same length", name, len(def.Models), len(def.Prompts))
			}
		case StrategyFusion:
			if len(def.Models) < 2 {
				return nil, fmt.Errorf("chain %q: fusion strategy needs at least 2 panel models", name)
			}
			if def.Judge == "" {
				return nil, fmt.Errorf("chain %q: fusion strategy requires a judge model", name)
			}
		default:
			return nil, fmt.Errorf("chain %q: unknown strategy %q (want \"sequential\" or \"fusion\")", name, def.Strategy)
		}

		reg[name] = Chain{Name: name, Strategy: strategy, Models: def.Models, Prompts: def.Prompts, Judge: def.Judge}
	}
	return reg, nil
}
