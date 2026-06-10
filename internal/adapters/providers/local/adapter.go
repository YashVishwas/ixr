// Package local implements pkg/provider.Provider for any locally hosted model
// that exposes an OpenAI-compatible HTTP API.
// Use this for vLLM, LM Studio, text-generation-webui, etc.
package local

import "github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"

// New returns a provider adapter pointing at baseURL.
// name is used in logs and CallEvents. apiKey may be empty.
func New(name, apiKey, baseURL string) *openaicompat.Adapter {
	return openaicompat.New(name, apiKey, baseURL, baseURL)
}
