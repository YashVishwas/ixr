package chain

import (
	"fmt"
	"strings"

	"github.com/YashVishwas/ixr/internal/adapters/config"
)

// Resolve turns an X-IXR-Chain header value into a []Stage.
// It first checks cfg for a named chain matching the header exactly.
// If no name matches it treats the header as a comma-separated model list.
func Resolve(header string, cfg map[string]config.ChainDef) ([]Stage, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, fmt.Errorf("chain: empty X-IXR-Chain header")
	}

	// Named chain lookup.
	if def, ok := cfg[header]; ok {
		return fromDef(def)
	}

	// Inline comma-separated model list: "qwen3-8b,claude-sonnet-4-6"
	return ParseInline(header)
}

// ParseInline parses a comma-separated model list into stages with no prompts.
func ParseInline(header string) ([]Stage, error) {
	parts := strings.Split(header, ",")
	stages := make([]Stage, 0, len(parts))
	for _, p := range parts {
		m := strings.TrimSpace(p)
		if m == "" {
			continue
		}
		stages = append(stages, Stage{Model: m})
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("chain: no models found in X-IXR-Chain header %q", header)
	}
	if len(stages) < 2 {
		return nil, fmt.Errorf("chain: at least 2 models required, got 1 (%s)", stages[0].Model)
	}
	return stages, nil
}

// fromDef converts a config.ChainDef into []Stage.
func fromDef(def config.ChainDef) ([]Stage, error) {
	if len(def.Models) < 2 {
		return nil, fmt.Errorf("chain: named chain must have at least 2 models")
	}
	stages := make([]Stage, len(def.Models))
	for i, m := range def.Models {
		stages[i] = Stage{Model: strings.TrimSpace(m)}
		if i < len(def.Prompts) {
			stages[i].Prompt = def.Prompts[i]
		}
	}
	return stages, nil
}
