// Package mistral implements pkg/provider.Provider for Mistral's
// OpenAI-compatible API (free tier, e.g. mistral-large-latest, codestral-latest).
package mistral

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://api.mistral.ai/v1"

// New returns a Mistral-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("mistral", apiKey, baseURL, defaultBaseURL)
}
