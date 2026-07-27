package ingress

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/internal/adapters/bus"
	"github.com/YashVishwas/ixr/internal/adapters/store/modelperf"
	"github.com/YashVishwas/ixr/internal/adapters/store/policystore"
	"github.com/YashVishwas/ixr/internal/domain/cache"
	"github.com/YashVishwas/ixr/internal/domain/chain"
	"github.com/YashVishwas/ixr/internal/domain/circuitbreaker"
	"github.com/YashVishwas/ixr/internal/domain/identity"
	"github.com/YashVishwas/ixr/internal/domain/routing"
	"github.com/YashVishwas/ixr/internal/domain/scoring"
	budgetplugin "github.com/YashVishwas/ixr/plugins/budget"
	"github.com/YashVishwas/ixr/pkg/guardrail"
	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// TestLoadProfile drives sustained, mixed, concurrent traffic through the
// real request pipeline — routing/scoring with the bandit live, circuit
// breaker, semantic cache, sequential + fusion chains, hierarchical budget —
// with fast stub providers standing in for the network call, and captures
// CPU/heap/goroutine/mutex/block profiles for offline analysis with
// `go tool pprof`. This is a profiling harness, not a correctness test:
// skipped by default so it never runs as part of the normal suite or gets
// counted alongside it.
//
//	IXR_LOADTEST=1 go test ./internal/ingress/ -run TestLoadProfile -v -timeout 5m
//
// Tunables: IXR_LOADTEST_WORKERS (default 200), IXR_LOADTEST_DURATION
// (default 30s, Go duration syntax), IXR_LOADTEST_PROFILE_DIR (default
// os.TempDir()).
func TestLoadProfile(t *testing.T) {
	if os.Getenv("IXR_LOADTEST") == "" {
		t.Skip("set IXR_LOADTEST=1 to run the sustained load/profile harness")
	}

	workers := loadtestEnvInt("IXR_LOADTEST_WORKERS", 200)
	duration := loadtestEnvDuration("IXR_LOADTEST_DURATION", 30*time.Second)
	profileDir := os.Getenv("IXR_LOADTEST_PROFILE_DIR")
	if profileDir == "" {
		profileDir = os.TempDir()
	}

	t.Logf("workers=%d duration=%s profileDir=%s", workers, duration, profileDir)

	// --- Build the real pipeline, stub only the network edge ---
	perfStore := modelperf.NewMemory()
	policyMem := policystore.NewMemory(nil)
	catalog := routing.Catalog()
	scoringEngine := scoring.NewEngine(perfStore, policyMem, catalog)
	bandit := scoring.NewEpsilonGreedy(0.1, scoring.DefaultRewardWeights)
	scoringEngine.SetBandit(bandit)

	cbRegistry := circuitbreaker.NewRegistry(circuitbreaker.Policy{
		SuccessRateThreshold: 0.90,
		WindowDuration:       10 * time.Second,
		MinRequests:          5,
		HalfOpenAfter:        2 * time.Second,
		ProbeCount:           1,
	})

	exactCache := &cache.ExactCache{Memory: cache.NewMemory(4096, 5*time.Minute)}
	semanticBackend := cache.NewMemorySemanticBackend(4096)
	responseCache := cache.NewSemanticCache(exactCache, semanticBackend, cache.WordVectorizer{}, 0.90)

	memBus := bus.NewMemory(256)
	ctx, cancelBus := context.WithCancel(context.Background())
	defer cancelBus()
	go memBus.Start(ctx)

	budgetLimits := map[string]budgetplugin.Limit{
		"tenant-a": {LimitUSD: 5.0, WarnAt: 0.8},
		"tenant-b": {LimitUSD: 5.0, WarnAt: 0.8},
	}
	budgetPlugin := budgetplugin.New(budgetLimits, memBus, "")
	defer budgetPlugin.Close()
	memBus.Subscribe(budgetPlugin)
	interceptors := guardrail.Chain{budgetPlugin}

	// A couple of "flaky" models exercise retry + circuit breaker + bandit
	// cooldown under real concurrent load, not just an isolated unit test.
	flaky := map[string]bool{"claude-3-opus-20240229": true, "gpt-4-turbo": true}
	router := Router(func(model string) (provider.Provider, error) {
		return &loadStubProvider{name: model, fail: flaky[model]}, nil
	})

	fusionChain := chain.Chain{
		Name:     "fusion-load",
		Strategy: chain.StrategyFusion,
		Models:   []string{"gpt-4o", "claude-haiku-4-5"},
		Judge:    "mistral-small-latest",
	}
	seqChain := chain.Chain{
		Name:    "seq-load",
		Models:  []string{"gpt-4o-mini", "gpt-4o"},
		Prompts: []string{"", "refine it"},
	}
	chains := chain.Registry{fusionChain.Name: fusionChain, seqChain.Name: seqChain}

	chatHandler := NewChatHandler(router, memBus,
		WithEngine(scoringEngine),
		WithCBRegistry(cbRegistry),
		WithChains(chains),
		WithRetryConfig(routing.RetryConfig{MaxAttempts: 3, InitialBackoff: 5 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, BackoffFactor: 2}),
	)
	cacheLayer := NewCacheMiddleware(responseCache, 5*time.Minute, chatHandler)
	handler := NewInterceptorMiddleware(interceptors, cacheLayer).WithBus(memBus)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	prompts := []string{
		"what is the capital of France?",
		"summarize the plan for next quarter",
		"write a haiku about the ocean",
		"explain how TCP handshakes work",
	}
	models := []string{"gpt-4o", "gpt-4o-mini", "claude-haiku-4-5", "claude-3-opus-20240229", "gpt-4-turbo", "auto", "fusion-load", "seq-load"}
	tenants := []string{"tenant-a", "tenant-b", "tenant-c"}

	// --- Baseline, before load ---
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	var baselineMem runtime.MemStats
	runtime.ReadMemStats(&baselineMem)

	cpuFile, err := os.Create(profileDir + "/ixr-loadtest-cpu.prof")
	if err != nil {
		t.Fatalf("create cpu profile: %v", err)
	}
	defer cpuFile.Close()
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("start cpu profile: %v", err)
	}

	// --- Sustained mixed concurrent load ---
	var wg sync.WaitGroup
	var requests, errorsN int64
	stop := time.Now().Add(duration)
	client := &http.Client{Timeout: 5 * time.Second}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for time.Now().Before(stop) {
				model := models[rng.Intn(len(models))]
				prompt := prompts[rng.Intn(len(prompts))]
				tenant := tenants[rng.Intn(len(tenants))]
				stream := rng.Intn(10) == 0 // 10% streaming

				body := fmt.Sprintf(`{"model":%q,"stream":%v,"messages":[{"role":"user","content":%q}]}`,
					model, stream, prompt)
				req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-IXR-Tenant", tenant)
				req = req.WithContext(identity.WithIdentity(req.Context(), schema.Identity{TenantID: tenant, UserID: fmt.Sprintf("user-%d", id%5)}))

				atomic.AddInt64(&requests, 1)
				resp, err := client.Do(req)
				if err != nil {
					atomic.AddInt64(&errorsN, 1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode >= 500 {
					atomic.AddInt64(&errorsN, 1)
				}
			}
		}(w)
	}
	wg.Wait()
	pprof.StopCPUProfile()

	t.Logf("requests=%d errors=%d (%.1f%%)", requests, errorsN, 100*float64(errorsN)/float64(requests))

	// --- Heap profile, post-load ---
	runtime.GC()
	var afterMem runtime.MemStats
	runtime.ReadMemStats(&afterMem)
	t.Logf("heap in-use: baseline=%.2fMB after=%.2fMB delta=%.2fMB (HeapObjects baseline=%d after=%d)",
		float64(baselineMem.HeapInuse)/1e6, float64(afterMem.HeapInuse)/1e6,
		float64(afterMem.HeapInuse-baselineMem.HeapInuse)/1e6,
		baselineMem.HeapObjects, afterMem.HeapObjects)

	heapFile, err := os.Create(profileDir + "/ixr-loadtest-heap.prof")
	if err != nil {
		t.Fatalf("create heap profile: %v", err)
	}
	defer heapFile.Close()
	if err := pprof.WriteHeapProfile(heapFile); err != nil {
		t.Fatalf("write heap profile: %v", err)
	}

	// --- Mutex + block profiles ---
	if mutexFile, err := os.Create(profileDir + "/ixr-loadtest-mutex.prof"); err == nil {
		defer mutexFile.Close()
		_ = pprof.Lookup("mutex").WriteTo(mutexFile, 0)
	}
	if blockFile, err := os.Create(profileDir + "/ixr-loadtest-block.prof"); err == nil {
		defer blockFile.Close()
		_ = pprof.Lookup("block").WriteTo(blockFile, 0)
	}

	// --- Goroutine-leak check: let in-flight background work settle, then
	// compare against baseline. Some slack is expected (test/runtime
	// internals, httptest server's own goroutines) — a leak looks like
	// "grows with worker count and never comes back down", not a fixed
	// small delta.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	afterGoroutines := runtime.NumGoroutine()
	t.Logf("goroutines: baseline=%d after=%d delta=%d", baselineGoroutines, afterGoroutines, afterGoroutines-baselineGoroutines)

	if afterGoroutines-baselineGoroutines > 50 {
		if goroFile, err := os.Create(profileDir + "/ixr-loadtest-goroutine.prof"); err == nil {
			defer goroFile.Close()
			_ = pprof.Lookup("goroutine").WriteTo(goroFile, 2)
			t.Logf("goroutine delta exceeds 50 — full stacks written to %s/ixr-loadtest-goroutine.prof", profileDir)
		}
	}

	t.Logf("profiles written to %s: ixr-loadtest-{cpu,heap,mutex,block}.prof", profileDir)
}

// loadStubProvider simulates realistic network latency and an optional
// persistent failure mode (to exercise retry/circuit-breaker/bandit-cooldown
// under real concurrent load).
type loadStubProvider struct {
	name string
	fail bool
}

func (s *loadStubProvider) Name() string { return s.name }

// sleepOrCancel simulates network latency the way a real provider adapter's
// http.NewRequestWithContext call actually behaves: the wait aborts the
// instant ctx is canceled, instead of unconditionally sleeping the full
// duration regardless of whether the caller is still there.
func sleepOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *loadStubProvider) Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error) {
	if err := sleepOrCancel(ctx, time.Duration(20+rand.Intn(80))*time.Millisecond); err != nil {
		return nil, err
	}
	if s.fail {
		return nil, fmt.Errorf("%s: status 503: simulated upstream failure", s.name)
	}
	return &schema.ResponseEnvelope{
		ID:      "loadtest-" + strconv.Itoa(rand.Int()),
		Model:   s.name,
		Choices: []schema.Choice{{Message: schema.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		Usage:   schema.Usage{PromptTokens: 12, CompletionTokens: 8, TotalTokens: 20},
	}, nil
}

func (s *loadStubProvider) Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(provider.StreamChunk) error) error {
	if err := sleepOrCancel(ctx, time.Duration(20+rand.Intn(80))*time.Millisecond); err != nil {
		return err
	}
	if s.fail {
		return fmt.Errorf("%s: status 503: simulated upstream failure", s.name)
	}
	if err := fn(provider.StreamChunk{ID: "loadtest", Delta: schema.Message{Role: "assistant", Content: "ok"}}); err != nil {
		return err
	}
	return fn(provider.StreamChunk{ID: "loadtest", Usage: &schema.Usage{PromptTokens: 12, CompletionTokens: 8, TotalTokens: 20}})
}

func loadtestEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func loadtestEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
