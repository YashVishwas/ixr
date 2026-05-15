// Package openrouter implements pkg/provider.Provider for OpenRouter's
// OpenAI-compatible API (free-tier models such as qwen/qwen3-coder:free).
package openrouter

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

var defaultHeaders = map[string]string{
	"HTTP-Referer": "https://github.com/YashVishwas/ixr",
	"X-Title":      "ixr",
}

// New returns an OpenRouter-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.NewWithHeaders("openrouter", apiKey, baseURL, defaultBaseURL, defaultHeaders)
}
