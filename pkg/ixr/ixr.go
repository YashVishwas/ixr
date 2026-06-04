// Package ixr is the one-line entry point for embedding ixr in a Go service.
//
//	import ixr "github.com/YashVishwas/ixr/pkg/ixr"
//
//	func main() {
//	    go ixr.Start()
//	}
package ixr

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	auditlog "github.com/YashVishwas/ixr/plugins/audit-log"
	"github.com/YashVishwas/ixr/plugins/telemetry"

	"github.com/YashVishwas/ixr/internal/adapters/bus"
	cfgloader "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/internal/adapters/pluginmgr"
	"github.com/YashVishwas/ixr/internal/adapters/providers/anthropic"
	"github.com/YashVishwas/ixr/internal/adapters/providers/cerebras"
	"github.com/YashVishwas/ixr/internal/adapters/providers/deepseek"
	githubmodels "github.com/YashVishwas/ixr/internal/adapters/providers/githubmodels"
	"github.com/YashVishwas/ixr/internal/adapters/providers/googleai"
	"github.com/YashVishwas/ixr/internal/adapters/providers/llama"
	"github.com/YashVishwas/ixr/internal/adapters/providers/mistral"
	"github.com/YashVishwas/ixr/internal/adapters/providers/openai"
	"github.com/YashVishwas/ixr/internal/adapters/providers/openrouter"
	"github.com/YashVishwas/ixr/internal/adapters/providers/sambanova"
	"github.com/YashVishwas/ixr/internal/adapters/providers/zhipu"
	modelperf "github.com/YashVishwas/ixr/internal/adapters/store/modelperf"
	policystore "github.com/YashVishwas/ixr/internal/adapters/store/policystore"
	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/policy"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/internal/ingress"
	"github.com/YashVishwas/ixr/pkg/provider"
)

// Option configures the ixr instance.
type Option func(*config)

type config struct {
	port       int
	configFile string
}

// WithPort overrides the listen port (default: 7000).
func WithPort(port int) Option {
	return func(c *config) { c.port = port }
}

// WithConfigFile loads configuration from the given ixr.yaml path.
// Provider credentials in the file may use ${ENV_VAR} syntax.
func WithConfigFile(path string) Option {
	return func(c *config) { c.configFile = path }
}

// Start starts the ixr proxy and blocks until the process receives SIGINT/SIGTERM
// or a fatal error occurs. It is the one-line entry point for embedding ixr.
func Start(opts ...Option) error {
	cfg := &config{port: 7000}
	for _, o := range opts {
		o(cfg)
	}

	registry, fileCfg, port, err := buildRegistry(cfg)
	if err != nil {
		return err
	}

	// --- Stores ---
	perfStore := modelperf.NewMemory()
	policyMem := policystore.NewMemory(nil)

	// --- Circuit breaker ---
	cbRegistry := circuitbreaker.NewRegistry(circuitbreaker.DefaultPolicy)

	// --- Scoring engine ---
	scoringEngine := scoring.NewEngine(perfStore, policyMem, routing.Catalog())

	// --- Identity resolver ---
	resolver := &identity.Resolver{}

	// --- Router (model name → provider) ---
	router := buildRouter(registry)

	// --- Event bus + plugins ---
	memBus := bus.NewMemory(0)
	mgr := pluginmgr.New(memBus)
	mgr.Register(&auditlog.Plugin{})
	mgr.Register(telemetry.New(perfStore, telemetry.NewJSONLinesSink(os.Stderr)))

	// --- Adaptive routing ---
	bandit := scoring.NewEpsilonGreedy(0.1, scoring.DefaultRewardWeights)
	shadowOrch := scoring.NewOrchestrator(perfStore, scoring.DefaultRewardWeights, bandit)

	// --- Chat handler ---
	chatHandler := ingress.NewChatHandler(
		router,
		memBus,
		ingress.WithEngine(scoringEngine),
		ingress.WithCBRegistry(cbRegistry),
		ingress.WithShadow(shadowOrch),
	)

	// --- Rate limiter ---
	var rl policy.RateLimiter
	if fileCfg != nil {
		rl = policy.NewMemoryRateLimiter(
			fileCfg.RateLimit.MaxRequests,
			fileCfg.RateLimit.MaxTokens,
			time.Duration(fileCfg.RateLimit.WindowSec)*time.Second,
		)
	} else {
		rl = policy.NewMemoryRateLimiter(0, 0, 60*time.Second)
	}
	rateMW := ingress.NewRateLimitMiddleware(rl)

	// --- Auth middleware ---
	var authMW *ingress.AuthMiddleware
	if fileCfg != nil {
		authMW = ingress.NewAuthMiddleware(fileCfg.Auth, resolver)
	} else {
		authMW = ingress.NewAuthMiddleware(cfgloader.AuthConfig{DisableAuth: true}, resolver)
	}

	// --- Middleware chain: auth → rate limit → chat ---
	mux := http.NewServeMux()
	chain := authMW.Handler(rateMW.Handler(chatHandler))
	mux.Handle("POST /v1/chat/completions", chain)

	// --- Config hot reload ---
	if cfg.configFile != "" && fileCfg != nil {
		_, _ = cfgloader.Watch(cfg.configFile, func(newCfg *cfgloader.Config, watchErr error) {
			if watchErr != nil || newCfg == nil {
				return
			}
			authMW.Reload(newCfg.Auth)
			rl.Reload(newCfg.RateLimit)
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go memBus.Start(ctx)

	// --- Server (TLS or plain) ---
	if fileCfg != nil && fileCfg.Server.TLSCert != "" {
		srv, tlsErr := ingress.NewServerTLS(port, mux, fileCfg.Server.TLSCert, fileCfg.Server.TLSKey, fileCfg.Server.ClientCA)
		if tlsErr != nil {
			return fmt.Errorf("TLS setup: %w", tlsErr)
		}
		return srv.Run(ctx)
	}
	return ingress.NewServer(port, mux).Run(ctx)
}

// buildRouter constructs the prefix-based model→provider dispatch function.
func buildRouter(registry map[string]provider.Provider) ingress.Router {
	return func(model string) (provider.Provider, error) {
		m := strings.ToLower(model)
		switch {
		case strings.HasPrefix(m, "gpt-oss"):
			p, ok := registry["cerebras"]
			if !ok {
				return nil, fmt.Errorf("cerebras provider not configured (use CEREBRAS_API_KEY or ixr.yaml providers.cerebras)")
			}
			return p, nil
		case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3"):
			p, ok := registry["openai"]
			if !ok {
				return nil, fmt.Errorf("openai provider not configured")
			}
			return p, nil
		case strings.HasPrefix(model, "claude-"):
			p, ok := registry["anthropic"]
			if !ok {
				return nil, fmt.Errorf("anthropic provider not configured")
			}
			return p, nil
		case strings.HasPrefix(m, "gemma") || strings.Contains(m, "gemma-"):
			p, ok := registry["gemma"]
			if !ok {
				return nil, fmt.Errorf("gemma provider not configured")
			}
			return p, nil
		case strings.HasPrefix(m, "gemini"):
			p, ok := registry["gemini"]
			if !ok {
				return nil, fmt.Errorf("gemini provider not configured")
			}
			return p, nil
		case strings.HasPrefix(m, "openai/"):
			p, ok := registry["github"]
			if !ok {
				return nil, fmt.Errorf("github provider not configured (use GITHUB_TOKEN or ixr.yaml providers.github)")
			}
			return p, nil
		case strings.Contains(m, "/"):
			p, ok := registry["openrouter"]
			if !ok {
				return nil, fmt.Errorf("openrouter provider not configured (use OPENROUTER_API_KEY or ixr.yaml providers.openrouter)")
			}
			return p, nil
		case strings.HasPrefix(m, "mistral-") || strings.HasPrefix(m, "codestral") ||
			strings.HasPrefix(m, "magistral") || strings.HasPrefix(m, "devstral"):
			p, ok := registry["mistral"]
			if !ok {
				return nil, fmt.Errorf("mistral provider not configured (use MISTRAL_API_KEY or ixr.yaml providers.mistral)")
			}
			return p, nil
		case m == "gemma-3-12b-it":
			p, ok := registry["sambanova"]
			if !ok {
				return nil, fmt.Errorf("sambanova provider not configured (use SAMBANOVA_API_KEY or ixr.yaml providers.sambanova)")
			}
			return p, nil
		case strings.HasPrefix(m, "meta-llama"):
			p, ok := registry["sambanova"]
			if !ok {
				return nil, fmt.Errorf("sambanova provider not configured (use SAMBANOVA_API_KEY or ixr.yaml providers.sambanova)")
			}
			return p, nil
		case m == "deepseek-v3.2":
			p, ok := registry["sambanova"]
			if !ok {
				return nil, fmt.Errorf("sambanova provider not configured (use SAMBANOVA_API_KEY or ixr.yaml providers.sambanova)")
			}
			return p, nil
		case strings.HasPrefix(m, "qwen3") || strings.HasPrefix(m, "qwen-3"):
			p, ok := registry["cerebras"]
			if !ok {
				return nil, fmt.Errorf("cerebras provider not configured (use CEREBRAS_API_KEY or ixr.yaml providers.cerebras)")
			}
			return p, nil
		case strings.HasPrefix(m, "llama-4-maverick"):
			p, ok := registry["cerebras"]
			if !ok {
				return nil, fmt.Errorf("cerebras provider not configured (use CEREBRAS_API_KEY or ixr.yaml providers.cerebras)")
			}
			return p, nil
		case strings.HasPrefix(m, "glm-"):
			p, ok := registry["zhipu"]
			if !ok {
				return nil, fmt.Errorf("zhipu provider not configured (use ZHIPU_API_KEY or ixr.yaml providers.zhipu)")
			}
			return p, nil
		case strings.Contains(m, "llama"):
			p, ok := registry["llama"]
			if !ok {
				return nil, fmt.Errorf("llama provider not configured (use GROQ_API_KEY or ixr.yaml providers.llama)")
			}
			return p, nil
		case strings.HasPrefix(m, "deepseek"):
			p, ok := registry["deepseek"]
			if !ok {
				return nil, fmt.Errorf("deepseek provider not configured")
			}
			return p, nil
		default:
			return nil, fmt.Errorf("no provider found for model %q", model)
		}
	}
}

// buildRegistry constructs the provider map and effective port from config file or env vars.
func buildRegistry(cfg *config) (map[string]provider.Provider, *cfgloader.Config, int, error) {
	var fileCfg *cfgloader.Config
	var err error

	if cfg.configFile != "" {
		fileCfg, err = cfgloader.Load(cfg.configFile)
		if err != nil {
			return nil, nil, 0, err
		}
	} else {
		fileCfg, err = cfgloader.Discover()
		if err != nil {
			return nil, nil, 0, err
		}
	}

	registry := map[string]provider.Provider{}
	port := cfg.port

	if fileCfg != nil {
		if fileCfg.Server.Port != 0 && cfg.port == 7000 {
			port = fileCfg.Server.Port
		}
		for name, pc := range fileCfg.Providers {
			switch name {
			case "openai":
				if pc.APIKey != "" {
					registry["openai"] = openai.New(pc.APIKey, pc.BaseURL)
				}
			case "anthropic":
				if pc.APIKey != "" {
					registry["anthropic"] = anthropic.New(pc.APIKey, pc.BaseURL)
				}
			case "gemini":
				if pc.APIKey != "" {
					registry["gemini"] = googleai.NewGemini(pc.APIKey, pc.BaseURL)
				}
			case "gemma":
				if pc.APIKey != "" {
					registry["gemma"] = googleai.NewGemma(pc.APIKey, pc.BaseURL)
				}
			case "llama":
				if pc.APIKey != "" {
					registry["llama"] = llama.New(pc.APIKey, pc.BaseURL)
				}
			case "deepseek":
				if pc.APIKey != "" {
					registry["deepseek"] = deepseek.New(pc.APIKey, pc.BaseURL)
				}
			case "cerebras":
				if pc.APIKey != "" {
					registry["cerebras"] = cerebras.New(pc.APIKey, pc.BaseURL)
				}
			case "mistral":
				if pc.APIKey != "" {
					registry["mistral"] = mistral.New(pc.APIKey, pc.BaseURL)
				}
			case "openrouter":
				if pc.APIKey != "" {
					registry["openrouter"] = openrouter.New(pc.APIKey, pc.BaseURL)
				}
			case "sambanova":
				if pc.APIKey != "" {
					registry["sambanova"] = sambanova.New(pc.APIKey, pc.BaseURL)
				}
			case "github":
				if pc.APIKey != "" {
					registry["github"] = githubmodels.New(pc.APIKey, pc.BaseURL)
				}
			case "zhipu":
				if pc.APIKey != "" {
					registry["zhipu"] = zhipu.New(pc.APIKey, pc.BaseURL)
				}
			}
		}
	}

	// Env vars supplement or override config file.
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		registry["openai"] = openai.New(key, "")
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		registry["anthropic"] = anthropic.New(key, "")
	}
	googleKey := os.Getenv("GOOGLE_API_KEY")
	if googleKey == "" {
		googleKey = os.Getenv("GEMINI_API_KEY")
	}
	if googleKey != "" {
		registry["gemini"] = googleai.NewGemini(googleKey, "")
		registry["gemma"] = googleai.NewGemma(googleKey, "")
	}
	if key := os.Getenv("GROQ_API_KEY"); key != "" {
		registry["llama"] = llama.New(key, "")
	}
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		registry["deepseek"] = deepseek.New(key, "")
	}
	if key := os.Getenv("CEREBRAS_API_KEY"); key != "" {
		registry["cerebras"] = cerebras.New(key, "")
	}
	if key := os.Getenv("MISTRAL_API_KEY"); key != "" {
		registry["mistral"] = mistral.New(key, "")
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		registry["openrouter"] = openrouter.New(key, "")
	}
	if key := os.Getenv("SAMBANOVA_API_KEY"); key != "" {
		registry["sambanova"] = sambanova.New(key, "")
	}
	if key := os.Getenv("GITHUB_TOKEN"); key != "" {
		registry["github"] = githubmodels.New(key, "")
	}
	if key := os.Getenv("ZHIPU_API_KEY"); key != "" {
		registry["zhipu"] = zhipu.New(key, "")
	}

	if len(registry) == 0 {
		return nil, nil, 0, fmt.Errorf("ixr: no providers configured — set API keys (e.g. OPENAI_API_KEY, GROQ_API_KEY, CEREBRAS_API_KEY, OPENROUTER_API_KEY) or provide ixr.yaml")
	}

	return registry, fileCfg, port, nil
}
