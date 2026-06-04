// Package ollama implements pkg/provider.Provider for Ollama's local OpenAI-compatible API.
// Default endpoint: http://localhost:11434/v1
package ollama

import "github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"

const defaultBase = "http://localhost:11434/v1"

// New returns a provider adapter for a locally running Ollama instance.
// apiKey is optional (Ollama doesn't require one by default).
func New(apiKey, baseURL string) *openaicompat.Adapter {
	return openaicompat.New("ollama", apiKey, baseURL, defaultBase)
}
