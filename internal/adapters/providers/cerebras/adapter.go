// Package cerebras implements pkg/provider.Provider for Cerebras's
// OpenAI-compatible API (free tier, e.g. qwen3-235b, qwen-3-coder-480b).
package cerebras

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://api.cerebras.ai/v1"

// New returns a Cerebras-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("cerebras", apiKey, baseURL, defaultBaseURL)
}
