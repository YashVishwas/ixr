// Package deepseek implements pkg/provider.Provider for DeepSeek's
// OpenAI-compatible API (free-tier models such as deepseek-chat).
package deepseek

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://api.deepseek.com/v1"

// New returns a DeepSeek-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("deepseek", apiKey, baseURL, defaultBaseURL)
}
