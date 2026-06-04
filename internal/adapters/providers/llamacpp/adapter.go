// Package llamacpp implements pkg/provider.Provider for the llama.cpp server's
// built-in OpenAI-compatible HTTP API. Default endpoint: http://localhost:8080/v1
package llamacpp

import "github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"

const defaultBase = "http://localhost:8080/v1"

// New returns a provider adapter for a locally running llama.cpp server.
func New(apiKey, baseURL string) *openaicompat.Adapter {
	return openaicompat.New("llamacpp", apiKey, baseURL, defaultBase)
}
