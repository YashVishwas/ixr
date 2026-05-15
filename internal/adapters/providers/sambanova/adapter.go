// Package sambanova implements pkg/provider.Provider for SambaNova's
// OpenAI-compatible API (free tier, e.g. Meta-Llama-3.3-70B-Instruct, DeepSeek-V3.2).
package sambanova

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://api.sambanova.ai/v1"

// New returns a SambaNova-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("sambanova", apiKey, baseURL, defaultBaseURL)
}
