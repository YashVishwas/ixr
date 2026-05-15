// Package zhipu implements pkg/provider.Provider for Zhipu (Z.ai) bigmodel.cn
// OpenAI-compatible API (free tier, e.g. glm-4.5-flash, glm-4.7-flash).
package zhipu

import (
	"github.com/YashVishwas/ixr/internal/adapters/providers/openaicompat"
	"github.com/YashVishwas/ixr/pkg/provider"
)

const defaultBaseURL = "https://open.bigmodel.cn/api/paas/v4"

// New returns a Zhipu-backed provider. Pass baseURL="" for the default host.
func New(apiKey, baseURL string) provider.Provider {
	return openaicompat.New("zhipu", apiKey, baseURL, defaultBaseURL)
}
