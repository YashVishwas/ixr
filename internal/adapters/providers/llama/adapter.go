// Package llama implements pkg/provider.Provider for Llama-family models
// via Groq's OpenAI-compatible API (free tier, e.g. llama-3.1-8b-instant).
package llama

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://api.groq.com/openai/v1"

// New returns a Groq-hosted Llama provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("llama", apiKey, baseURL, defaultBaseURL)
}
