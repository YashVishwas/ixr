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
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	auditlog "github.com/YashVishwas/ixr/plugins/audit-log"
	"github.com/YashVishwas/ixr/plugins/banditreward"
	"github.com/YashVishwas/ixr/plugins/brevity"
	budgetplugin "github.com/YashVishwas/ixr/plugins/budget"
	feedbackplugin "github.com/YashVishwas/ixr/plugins/feedback"
	memoryplugin "github.com/YashVishwas/ixr/plugins/memory"
	"github.com/YashVishwas/ixr/plugins/telemetry"

	"github.com/YashVishwas/ixr/internal/adapters/bus"
	cfgloader "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/internal/adapters/pluginmgr"
	"github.com/YashVishwas/ixr/internal/adapters/providers/anthropic"
	"github.com/YashVishwas/ixr/internal/adapters/providers/bedrock"
	"github.com/YashVishwas/ixr/internal/adapters/providers/cerebras"
	"github.com/YashVishwas/ixr/internal/adapters/providers/deepseek"
	githubmodels "github.com/YashVishwas/ixr/internal/adapters/providers/githubmodels"
	"github.com/YashVishwas/ixr/internal/adapters/providers/googleai"
	"github.com/YashVishwas/ixr/internal/adapters/providers/llama"
	"github.com/YashVishwas/ixr/internal/adapters/providers/llamacpp"
	"github.com/YashVishwas/ixr/internal/adapters/providers/local"
	"github.com/YashVishwas/ixr/internal/adapters/providers/mistral"
	"github.com/YashVishwas/ixr/internal/adapters/providers/ollama"
	"github.com/YashVishwas/ixr/internal/adapters/providers/openai"
	"github.com/YashVishwas/ixr/internal/adapters/providers/openrouter"
	"github.com/YashVishwas/ixr/internal/adapters/providers/sambanova"
	"github.com/YashVishwas/ixr/internal/adapters/providers/zhipu"
	modelperf "github.com/YashVishwas/ixr/internal/adapters/store/modelperf"
	policystore "github.com/YashVishwas/ixr/internal/adapters/store/policystore"
	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	domainfeedback "github.com/YashVishwas/ixr/internal/domain/feedback"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/memory"
	"github.com/YashVishwas/ixr/internal/domain/policy"
	"github.com/YashVishwas/ixr/internal/domain/retrieval"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	"github.com/YashVishwas/ixr/internal/domain/session"
	"github.com/YashVishwas/ixr/internal/ingress"
	"github.com/YashVishwas/ixr/internal/observability"
	pkgguardrail "github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/prometheus/client_golang/prometheus"
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

	// log_level was parsed into Config but never actually applied to the
	// logger anywhere — every deployment ran at Info regardless of what was
	// configured, silently dropping Debug-level diagnostics (the routing
	// decision logging below included).
	slog.SetLogLoggerLevel(logLevelFromConfig(fileCfg))

	// --- Observability ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	otlpEndpoint := os.Getenv("IXR_OTLP_ENDPOINT")
	shutdownTracer, tracerErr := observability.InitTracer(ctx, "ixr", otlpEndpoint)
	if tracerErr != nil {
		return fmt.Errorf("tracer init: %w", tracerErr)
	}
	defer func() { _ = shutdownTracer(context.Background()) }()

	promReg := prometheus.NewRegistry()
	metrics := observability.NewMetrics(promReg)

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

	// --- User memory store (optional) ---
	// Bounded by default (1h TTL, 50 entries/user, journal recompacted every
	// 15m) rather than accumulating forever — see
	// internal/domain/memory.defaultMemoryTTL for why.
	memoryTTL := time.Duration(envInt("IXR_MEMORY_TTL_SEC", 3600)) * time.Second
	memoryMaxPerUser := envInt("IXR_MEMORY_MAX_PER_USER", 50)
	memoryCompactInterval := time.Duration(envInt("IXR_MEMORY_COMPACT_INTERVAL_SEC", 900)) * time.Second
	memoryStore := memory.NewMemoryStoreWithLimits(os.Getenv("IXR_MEMORY_DIR"), memoryTTL, memoryMaxPerUser, memoryCompactInterval)
	defer memoryStore.Close()

	// --- Event bus + plugins ---
	memBus := bus.NewMemory(0)
	mgr := pluginmgr.New(memBus)
	mgr.Register(&auditlog.Plugin{})

	// Telemetry: always write JSON lines to stderr; add OTEL span sink when
	// IXR_OTLP_ENDPOINT is set so spans flow to any OTLP-compatible backend.
	var telemetrySink telemetry.Sink = telemetry.NewJSONLinesSink(os.Stderr)
	if otlpEndpoint != "" {
		telemetrySink = telemetry.MultiSink{
			telemetry.NewJSONLinesSink(os.Stderr),
			telemetry.NewOTELSink(),
		}
	}
	mgr.Register(telemetry.New(perfStore, telemetrySink))

	// Memory extraction: rule-based always on; LLM extractor added when
	// IXR_MEMORY=true and IXR_MEMORY_EXTRACTOR=llm (future — LLMCaller wiring
	// requires a provider reference; defaulting to rule-only for now).
	if os.Getenv("IXR_MEMORY") == "true" {
		ext := memory.RuleExtractor{}
		mgr.Register(memoryplugin.New(memoryStore, ext))
	}

	// --- Adaptive routing ---
	bandit := scoring.NewEpsilonGreedy(0.1, scoring.DefaultRewardWeights)
	shadowOrch := scoring.NewOrchestrator(perfStore, scoring.DefaultRewardWeights, bandit)

	// Bandit-driven primary auto-routing (RFC Gap 12) — opt-in and off by
	// default, so model:"auto" stays fully deterministic (and existing
	// deployments see no behavior change) unless explicitly enabled. Shares
	// the same bandit instance as shadow routing above, so exploration and
	// reward from both paths reinforce one set of arm statistics.
	var feedbackStore *domainfeedback.Store
	if os.Getenv("IXR_AUTO_BANDIT") == "true" {
		scoringEngine.SetBandit(bandit)
		mgr.Register(banditreward.New(bandit))

		// Caller feedback (POST /v1/feedback) strengthens the same
		// bandit's quality signal beyond banditreward's coarse, free
		// FinishReason proxy — a real human rating, when a caller
		// chooses to submit one. Indexing (feedbackplugin) and the HTTP
		// handler (registered below, alongside the other non-chat
		// endpoints) share one Store instance. Meaningless without
		// auto-routing enabled, so it's gated the same way.
		feedbackStore = domainfeedback.NewStore(envInt("IXR_FEEDBACK_STORE_SIZE", 10_000), time.Duration(envInt("IXR_FEEDBACK_TTL_SEC", 3600))*time.Second)
		mgr.Register(feedbackplugin.New(feedbackStore))
	}

	// --- Semantic cache ---
	cacheSize := envInt("IXR_CACHE_SIZE", 1024)
	cacheTTL := time.Duration(envInt("IXR_CACHE_TTL_SEC", 300)) * time.Second
	exactCache := &cache.ExactCache{Memory: cache.NewMemory(cacheSize, cacheTTL)}

	var responseCache cache.RequestAwareCache = exactCache
	if os.Getenv("IXR_SEMANTIC_CACHE") == "true" {
		threshold := float32(envFloat("IXR_CACHE_THRESHOLD", 0.92))
		semanticBackend := cache.NewPersistentSemanticBackend(cacheSize, os.Getenv("IXR_CACHE_DIR"))
		defer semanticBackend.Close()
		semanticCache := cache.NewSemanticCache(exactCache, semanticBackend, cache.WordVectorizer{}, threshold)

		// Quality tier (opt-in): catches paraphrase-level matches the fast
		// WordVectorizer token-overlap approximation misses (e.g. "summarize
		// this" vs "give me a summary"), by embedding through a real
		// provider on Store (async, off the request path) and consulting it
		// on Lookup only as a fallback after the fast tier misses, bounded by
		// IXR_CACHE_QUALITY_TIMEOUT_MS rather than the fast path's fixed 5ms.
		// Requires an OpenAI provider to already be configured — this reuses
		// that adapter rather than standing up a separate credential path.
		if os.Getenv("IXR_CACHE_QUALITY_EMBEDDER") == "true" {
			if embedder, ok := registry["openai"].(provider.Embedder); ok {
				model := envString("IXR_CACHE_QUALITY_MODEL", "text-embedding-3-small")
				qualityBackend := cache.NewMemorySemanticBackend(cacheSize)
				timeout := time.Duration(envInt("IXR_CACHE_QUALITY_TIMEOUT_MS", 150)) * time.Millisecond
				semanticCache.WithQualityTier(cache.ProviderEmbedder{Provider: embedder, Model: model}, qualityBackend, timeout)
			} else {
				slog.Warn("IXR_CACHE_QUALITY_EMBEDDER=true but no OpenAI provider is configured; quality tier disabled")
			}
		}

		responseCache = semanticCache
	}

	// --- Model chains (RFC Gap 11: sequential; Gap 11 fusion extension:
	// parallel-panel-plus-judge, matching OmniRoute's fusion/pipeline
	// routing strategies) ---
	chainDefs := make(map[string]chain.Def)
	if fileCfg != nil {
		for name, c := range fileCfg.Chains {
			chainDefs[name] = chain.Def{Strategy: c.Strategy, Models: c.Models, Prompts: c.Prompts, Judge: c.Judge}
		}
	}
	chainRegistry, err := chain.BuildRegistry(chainDefs)
	if err != nil {
		return fmt.Errorf("chains config: %w", err)
	}

	// --- Reversible compression store (optional) ---
	// Constructed before the chat handler because WithRetrieval is a
	// construction-time option — the compressor (wired further below,
	// alongside the rest of the interceptor chain) writes into the same
	// store, so both sides need the one instance.
	var retrievalStore *retrieval.Store
	if os.Getenv("IXR_COMPRESS_REQUESTS") == "true" && os.Getenv("IXR_COMPRESS_REVERSIBLE") == "true" {
		retrievalStoreSize := envInt("IXR_COMPRESS_RETRIEVAL_STORE_SIZE", 1000)
		retrievalStore = retrieval.NewStore(retrievalStoreSize)
	}

	// --- Chat handler ---
	chatHandler := ingress.NewChatHandler(
		router,
		memBus,
		ingress.WithEngine(scoringEngine),
		ingress.WithCBRegistry(cbRegistry),
		ingress.WithShadow(shadowOrch),
		ingress.WithMetrics(metrics),
		ingress.WithChains(chainRegistry),
		ingress.WithRetrieval(retrievalStore),
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

	// --- Mux ---
	mux := http.NewServeMux()

	// Observability stack wrapping: requestID → trace → handler
	obs := func(h http.Handler) http.Handler {
		return observability.RequestIDMiddleware(observability.TraceMiddleware(h))
	}

	// --- Budget enforcement — hierarchical (org → team → user) ---
	var interceptors pkgguardrail.Chain
	budgetLimits := make(map[string]budgetplugin.Limit)
	if fileCfg != nil {
		for tenantID, tc := range fileCfg.Tenants {
			// Org-level ceiling.
			if tc.Quotas.MonthlyUSDCap > 0 {
				budgetLimits[tenantID] = budgetplugin.Limit{
					LimitUSD: tc.Quotas.MonthlyUSDCap,
					WarnAt:   0.8,
				}
			}
			// Team-level ceilings: tenantID:teamID
			for teamID, team := range tc.Teams {
				if team.Quotas.MonthlyUSDCap > 0 {
					budgetLimits[tenantID+":"+teamID] = budgetplugin.Limit{
						LimitUSD: team.Quotas.MonthlyUSDCap,
						WarnAt:   0.8,
					}
				}
			}
		}
	}
	budgetPlugin := budgetplugin.New(budgetLimits, memBus, os.Getenv("IXR_BUDGET_DIR"))
	defer budgetPlugin.Close()
	mgr.Register(budgetPlugin) // accumulates spend post-call
	if len(budgetLimits) > 0 {
		interceptors = append(interceptors, budgetPlugin) // gates pre-call
	}

	// Request compression (opt-in): shrinks oversized tool-result/history
	// content before routing, caching, or the provider ever see it.
	// guardrail.RequestInterceptor already runs pre-cache/pre-routing and
	// InterceptorMiddleware already re-marshals a mutated req back into the
	// body, so this reuses that existing extension point rather than adding
	// a new one — see plugins/compressor's package doc for why.
	//
	// Reversible mode (IXR_COMPRESS_REVERSIBLE=true) additionally stores
	// truncated originals in retrievalStore (constructed above, before the
	// chat handler) — see plugins/compressor's package doc and
	// internal/domain/retrieval for how the model can ask for the full
	// content back rather than lose it.
	if os.Getenv("IXR_COMPRESS_REQUESTS") == "true" {
		maxChars := envInt("IXR_COMPRESS_MAX_CHARS", 0) // 0 → compressor.New's own default
		if retrievalStore != nil {
			retrievalTTL := time.Duration(envInt("IXR_COMPRESS_RETRIEVAL_TTL_SEC", 300)) * time.Second
			interceptors = append(interceptors, compressor.NewReversible(maxChars, retrievalStore, retrievalTTL))
		} else {
			interceptors = append(interceptors, compressor.New(maxChars))
		}
	}

	// Output-side brevity steering (opt-in): appends a terseness instruction
	// to the system message so the model favors fragments over prose,
	// trimming output tokens — the counterpart to input-side compression.
	// Whether it actually reduces tokens is model behavior, not something
	// this wiring can guarantee; see plugins/brevity's package doc.
	if os.Getenv("IXR_TERSE_OUTPUT") == "true" {
		interceptors = append(interceptors, brevity.New(os.Getenv("IXR_TERSE_INSTRUCTION")))
	}

	// --- Session store (optional) ---
	// Config file takes precedence; env vars are the fallback.
	sessionTTLSec := envInt("IXR_SESSION_TTL_SEC", 0)
	sessionMaxTurns := envInt("IXR_SESSION_MAX_TURNS", 50)
	sessionDir := os.Getenv("IXR_SESSION_DIR")
	if fileCfg != nil && fileCfg.Session.TTLSec > 0 {
		sessionTTLSec = fileCfg.Session.TTLSec
		if fileCfg.Session.MaxTurns > 0 {
			sessionMaxTurns = fileCfg.Session.MaxTurns
		}
		if fileCfg.Session.Dir != "" {
			sessionDir = fileCfg.Session.Dir
		}
	}

	cacheLayer := ingress.NewCacheMiddleware(responseCache, cacheTTL, chatHandler)
	var middleChain http.Handler = cacheLayer
	if sessionTTLSec > 0 {
		sessionTTL := time.Duration(sessionTTLSec) * time.Second
		var store session.SessionStore
		if sessionDir != "" {
			ps := session.NewPersistentSessionStore(sessionTTL, sessionMaxTurns, sessionDir)
			defer ps.Close()
			store = ps
		} else {
			mem := session.NewMemorySessionStore(sessionTTL, sessionMaxTurns)
			defer mem.Close()
			store = mem
		}
		middleChain = ingress.NewSessionMiddleware(store, cacheLayer)
	}

	// Admission control (RFC Gap 13, opt-in): caps aggregate in-flight
	// requests regardless of which model is requested — complementary to
	// per-model circuit breaking, not redundant with it. Checked before
	// rate limiting (which does per-identity accounting work of its own)
	// since there's no point doing that work if the system is already
	// globally saturated. 0/unset disables it entirely.
	admissionMW := ingress.NewAdmissionMiddleware(envInt("IXR_MAX_INFLIGHT", 0))

	// Chat completions: auth → admission control → rate limit → interceptors → memory → session → cache → chat
	// Memory sits outside session (per MemoryMiddleware's doc comment) so the
	// memory system message is in place before session history is appended.
	memTopK := envInt("IXR_MEMORY_TOP_K", 5)
	chatChain := authMW.Handler(admissionMW.Handler(rateMW.Handler(
		ingress.NewInterceptorMiddleware(interceptors,
			ingress.NewMemoryMiddleware(memoryStore, memTopK, middleChain),
		).WithBus(memBus),
	)))
	mux.Handle("POST /v1/chat/completions", obs(chatChain))

	// Non-chat endpoints: auth → handler (no caching)
	embeddingsHandler := ingress.NewEmbeddingsHandler(router)
	mux.Handle("POST /v1/embeddings", obs(authMW.Handler(embeddingsHandler)))

	imagesHandler := ingress.NewImagesHandler(router)
	mux.Handle("POST /v1/images/generations", obs(authMW.Handler(imagesHandler)))

	// Feedback (RFC Gap 12 quality signal) — only registered when
	// auto-bandit is enabled; feedbackStore is nil otherwise and there's
	// nothing meaningful for this endpoint to do.
	if feedbackStore != nil {
		feedbackHandler := ingress.NewFeedbackHandler(feedbackStore, bandit)
		mux.Handle("POST /v1/feedback", obs(authMW.Handler(feedbackHandler)))
	}

	// Schema and metrics are unauthenticated
	mux.Handle("GET /v1/schema", ingress.NewSchemaHandler())
	mux.Handle("GET /metrics", observability.Handler(promReg))

	// --- Config hot reload ---
	if cfg.configFile != "" && fileCfg != nil {
		_, _ = cfgloader.Watch(cfg.configFile, func(newCfg *cfgloader.Config, watchErr error) {
			if watchErr != nil || newCfg == nil {
				return
			}
			authMW.Reload(newCfg.Auth)
			rl.Reload(policy.RateLimit{
				WindowSec:   newCfg.RateLimit.WindowSec,
				MaxRequests: newCfg.RateLimit.MaxRequests,
				MaxTokens:   newCfg.RateLimit.MaxTokens,
			})
		})
	}

	go memBus.Start(ctx)

	// --- pprof (optional, off by default) ---
	// Deliberately not registered on the main mux: pprof exposes goroutine
	// stacks, heap contents, and lets a caller trigger an expensive CPU
	// profile — none of that belongs on the same listener as the public API.
	// IXR_PPROF_ADDR should be bound to localhost or an internal-only
	// interface (e.g. "127.0.0.1:6060"), never a public address.
	if addr := os.Getenv("IXR_PPROF_ADDR"); addr != "" {
		pprofMux := http.NewServeMux()
		pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
		pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		pprofSrv := &http.Server{Addr: addr, Handler: pprofMux}
		go func() {
			if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("pprof server failed", "err", err)
			}
		}()
		go func() {
			<-ctx.Done()
			_ = pprofSrv.Close()
		}()
		slog.Info("pprof debug server listening", "addr", addr)
	}

	// --- Request body size cap ---
	// Wraps the whole mux (not each handler individually) so a new
	// endpoint added later is covered automatically. Every current
	// JSON-decoding handler/middleware reads r.Body directly with no
	// limit of its own — several re-decode/re-marshal the same body more
	// than once as it passes through the interceptor/memory/session
	// chain, multiplying the cost of one oversized request. Defaults on
	// (unlike most opt-in features here) since this is a resource-
	// exhaustion guard, not a behavior change a caller would notice
	// until they send something unreasonably large; <=0 disables it.
	maxBodyBytes := int64(envInt("IXR_MAX_REQUEST_BODY_BYTES", 10<<20)) // 10MB
	bodyLimitMW := ingress.NewBodyLimitMiddleware(maxBodyBytes)
	limitedMux := http.NewServeMux()
	limitedMux.Handle("/", bodyLimitMW.Handler(mux))

	// --- Server (TLS or plain) ---
	if fileCfg != nil && fileCfg.Server.TLSCert != "" {
		srv, tlsErr := ingress.NewServerTLS(port, limitedMux, fileCfg.Server.TLSCert, fileCfg.Server.TLSKey, fileCfg.Server.ClientCA)
		if tlsErr != nil {
			return fmt.Errorf("TLS setup: %w", tlsErr)
		}
		return srv.Run(ctx)
	}
	return ingress.NewServer(port, limitedMux).Run(ctx)
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// buildRouter constructs the prefix-based model→provider dispatch function.
func buildRouter(registry map[string]provider.Provider) ingress.Router {
	return func(model string) (provider.Provider, error) {
		m := strings.ToLower(model)
		switch {
		// Local adapters take priority for explicitly local-prefixed model names
		case strings.HasPrefix(m, "ollama/"):
			p, ok := registry["ollama"]
			if !ok {
				return nil, fmt.Errorf("ollama provider not configured (start Ollama and set OLLAMA_BASE_URL or ixr.yaml providers.ollama)")
			}
			return p, nil
		case strings.HasPrefix(m, "llamacpp/"):
			p, ok := registry["llamacpp"]
			if !ok {
				return nil, fmt.Errorf("llamacpp provider not configured (start llama.cpp server and set LLAMACPP_BASE_URL or ixr.yaml providers.llamacpp)")
			}
			return p, nil
		case strings.HasPrefix(m, "local/"):
			p, ok := registry["local"]
			if !ok {
				return nil, fmt.Errorf("local provider not configured (set LOCAL_BASE_URL or ixr.yaml providers.local)")
			}
			return p, nil
		// AWS Bedrock
		case strings.HasPrefix(m, "amazon.") || strings.HasPrefix(m, "anthropic.") || strings.HasPrefix(m, "meta."):
			p, ok := registry["bedrock"]
			if !ok {
				return nil, fmt.Errorf("bedrock provider not configured (set AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION or ixr.yaml providers.bedrock)")
			}
			return p, nil
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

// logLevelFromConfig maps the config's log_level string to a slog.Level.
// Defaults to Info — same as slog's own zero-value default — when no config
// file was used or log_level wasn't set.
func logLevelFromConfig(fileCfg *cfgloader.Config) slog.Level {
	level := ""
	if fileCfg != nil {
		level = fileCfg.LogLevel
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
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
			case "ollama":
				registry["ollama"] = ollama.New(pc.APIKey, pc.BaseURL)
			case "llamacpp":
				registry["llamacpp"] = llamacpp.New(pc.APIKey, pc.BaseURL)
			case "local":
				if pc.BaseURL != "" {
					registry["local"] = local.New("local", pc.APIKey, pc.BaseURL)
				}
			case "bedrock":
				// bedrock uses separate credential fields via env or config extension
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
	// AWS Bedrock: region + static credentials from environment
	if region := os.Getenv("AWS_REGION"); region == "" {
		_ = os.Getenv("AWS_DEFAULT_REGION") // tolerate either
	}
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = os.Getenv("AWS_DEFAULT_REGION")
	}
	if awsRegion != "" && os.Getenv("AWS_ACCESS_KEY_ID") != "" {
		registry["bedrock"] = bedrock.New(
			awsRegion,
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
			os.Getenv("AWS_SESSION_TOKEN"),
		)
	}
	// Local model adapters
	if base := os.Getenv("OLLAMA_BASE_URL"); base != "" {
		registry["ollama"] = ollama.New("", base)
	} else if _, alreadySet := registry["ollama"]; !alreadySet {
		// Register Ollama on default port if not explicitly configured; it may not be running.
		registry["ollama"] = ollama.New("", "")
	}
	if base := os.Getenv("LLAMACPP_BASE_URL"); base != "" {
		registry["llamacpp"] = llamacpp.New("", base)
	}
	if base := os.Getenv("LOCAL_BASE_URL"); base != "" {
		registry["local"] = local.New("local", os.Getenv("LOCAL_API_KEY"), base)
	}

	if len(registry) == 0 {
		return nil, nil, 0, fmt.Errorf("ixr: no providers configured — set API keys (e.g. OPENAI_API_KEY, GROQ_API_KEY, CEREBRAS_API_KEY, OPENROUTER_API_KEY) or provide ixr.yaml")
	}

	return registry, fileCfg, port, nil
}
