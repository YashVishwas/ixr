// Package chain implements named sequential model chains (RFC Gap 11):
// a request names a chain instead of a single model, and ixr runs a fixed
// sequence of models where each step's output feeds the next step's prompt.
package chain

import "fmt"

// Chain is a named sequence of model calls.
type Chain struct {
	Name    string
	Models  []string
	Prompts []string // same length as Models; Prompts[0] is typically ""
}

// Registry maps chain name -> Chain.
type Registry map[string]Chain

// Lookup returns the chain for name, or false if name isn't a registered chain.
func (r Registry) Lookup(name string) (Chain, bool) {
	c, ok := r[name]
	return c, ok
}

// BuildRegistry validates and constructs a Registry from raw name -> (models, prompts) pairs.
func BuildRegistry(defs map[string]struct {
	Models  []string
	Prompts []string
}) (Registry, error) {
	reg := make(Registry, len(defs))
	for name, def := range defs {
		if len(def.Models) == 0 {
			return nil, fmt.Errorf("chain %q: must define at least one model", name)
		}
		if len(def.Prompts) != len(def.Models) {
			return nil, fmt.Errorf("chain %q: models (%d) and prompts (%d) must be the same length", name, len(def.Models), len(def.Prompts))
		}
		reg[name] = Chain{Name: name, Models: def.Models, Prompts: def.Prompts}
	}
	return reg, nil
}
