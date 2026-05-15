// Package githubmodels implements pkg/provider.Provider for GitHub Models'
// OpenAI-compatible inference API (free tier, e.g. openai/gpt-4.1).
package githubmodels

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://models.github.ai/inference"

// New returns a GitHub Models-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("github", apiKey, baseURL, defaultBaseURL)
}
