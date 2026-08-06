# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Opt-in pprof debug server (`IXR_PPROF_ADDR`, e.g. `127.0.0.1:6060`), off by
  default. Deliberately not registered on the main mux — pprof exposes
  goroutine stacks and heap contents and lets a caller trigger an expensive
  CPU profile, none of which belongs on the same listener as the public API.
- A sustained-load profiling harness (`internal/ingress/loadprofile_test.go`,
  `IXR_LOADTEST=1`) that drives mixed concurrent traffic through the real
  request pipeline — routing/scoring with the bandit live, circuit breaker,
  semantic cache, sequential + fusion chains, hierarchical budget — with
  fast stub providers standing in for the network edge, capturing
  CPU/heap/goroutine/mutex/block profiles. Skipped by default; used to find
  the circuit-breaker bug below. At realistic concurrency for the test
  hardware (100–200 workers on 12 cores): 0 errors across ~477k requests,
  +6 goroutines, no heap growth — no leaks or races found.
- Concurrency stress tests exercising real production-shaped load (many
  goroutines, not the sequential single-caller pattern the existing test
  suite otherwise uses everywhere): `TestSemanticCache_ConcurrentLookupAndStore`
  (50 goroutines x 40 ops of interleaved Lookup/Store) and
  `TestConcurrentMultiTenantIsolation` (three tenants hammering
  Intercept/OnEvent simultaneously, asserting no spend leaks or blocks
  cross tenant boundaries). Both pass under `go test -race`; ran the full
  suite under `-race` as a baseline alongside them.
- Fusion chain strategy (RFC Gap 11 extension) and progressive bandit
  cooldown (RFC Gap 12 extension): the two remaining pieces of the OmniRoute
  parity gaps this RFC calls out by name — "18 named routing strategies
  including a bandit-driven auto-combo engine with progressive cooldown"
  and fusion/pipeline routing strategies. `chain.Chain` gains a `Strategy`
  field (`"sequential"` default, or `"fusion"`: panel models run in
  parallel against the original messages, then a `Judge` model synthesizes
  their answers; a single panel member failing doesn't abort the request,
  only an all-panel failure does). `EpsilonGreedy`/`UCB` exclude an arm
  from `Select` after 3 consecutive failures for a backoff window that
  doubles per additional failure (capped at 5 minutes); a success clears
  it immediately; if every candidate is cooling down, `Select` falls back
  to the full list rather than starving routing (`internal/domain/scoring/bandit.go`).
  See `docs/rfc/0001-semantic-cache.md` Gap 12 for the reward-threshold
  coupling this relies on.
- Routing-failure logging: `writeError` (`internal/ingress/chat.go`) never
  logged anything, so every early-return failure across the 7 files that
  share it (auth, chat, chain, ratelimit, embeddings, images,
  schema_endpoint) was invisible server-side — a caller saw a 4xx/5xx, the
  server logged nothing. Now logs status/error_type/message at Warn (4xx)
  or Error (5xx). `log_level` in config was parsed but never applied to
  `slog` (every deployment ran at Info regardless); wired via
  `logLevelFromConfig`, applied once in `Start()`. Added a Debug-level
  "auto-routing decision" log (engine, primary, fallback chain, task hint
  scores) and a Warn specifically for `model:"auto"` picking a model with
  no configured provider — the fallback chain built for that decision is
  never consulted in this path (chat.go returns before `Execute()`, the
  only thing that walks it, is ever called), so the chain is logged
  anyway to show whether a viable candidate existed and was simply never
  tried. Observability only — does not change routing behavior.

### Fixed
- Circuit breaker outcome recording conflated a caller giving up
  (`context.Canceled`/`context.DeadlineExceeded`) with the provider actually
  failing — all four `RecordOutcome` call sites (`chat.go` and `chain.go`,
  streaming and non-streaming) used a bare `err == nil` check. A client
  disconnecting says nothing about whether a model is healthy, but under
  load-driven timeout pressure (found via a load-test profiling pass, not a
  design review) this could trip breakers on models that were never actually
  failing, removing routing capacity at exactly the point a cascade is most
  likely. Fixed with `shouldRecordOutcome` in `internal/ingress/chat.go`,
  gating all four call sites; a genuine provider error still trips the
  breaker as before. See `docs/rfc/0001-semantic-cache.md` Gap 13 for the
  related, still-open finding this surfaced: ixr has no system-level
  admission control independent of per-model circuit breaking.
- Context-window escalation (RFC Gap 5) was fully built and unit-tested
  but never actually invoked from the live request path: `chat.go` called
  providers directly and never routed through `routing.Execute`, so a real
  context-length overflow returned a raw 502 instead of escalating — for
  both `model:"auto"` (whose computed `FallbackChain` was read for
  `.Model` and then discarded) and any explicit catalog model. `chat.go`
  now computes a fallback chain up front (from the scoring engine for
  `auto`, or via new `routing.FallbackChainFor` for an explicit catalog
  model) and routes through `Execute`/`ExecuteStream` whenever one exists;
  a model outside the catalog keeps the old direct-call/502 behavior,
  since there's no `ContextWindow` data to escalate against. Also fixed
  two related executor bugs surfaced while wiring this up: `FallbackUsed`
  was computed from a loop index that resets after an escalation (always
  reporting `false` even when escalation succeeded), and an exhausted
  fallback chain returned a zero-value `Model`/`Provider`, misattributing
  the failure log to the original primary instead of whichever candidate
  actually produced the error. `schema.CallEvent` gains
  `FallbackUsed`/`FallbackFrom`, threaded through to `plugins/telemetry`.
  Found while auditing a stale integration branch that had never been
  merged despite fixing a live bug — see `docs/rfc/0001-semantic-cache.md`
  Gap 5.
- Shadow-routed requests bypassed reasoning-model token budget adjustment
  (Gap 6) and OTEL shadow tagging (Gap 7): `runShadow` sent the shadow
  model the caller's raw `max_tokens` instead of calling
  `reasoning.AdjustTokenBudget`, and `plugins/telemetry` never populated
  the `Shadow`/`ShadowOf` fields it already had, so shadow-routed calls
  were indistinguishable from primary calls in any OTLP dashboard built on
  these spans (silently double-counting tokens/cost whenever shadow
  routing was active).
- Google AI (Gemini/Gemma) and Bedrock adapters silently dropped image
  content on vision requests: their message-translation loops only ever
  read `m.Content`, never `m.Parts`, so the request still went through as
  text-only (not erroring or corrupting the body — the documented
  graceful-degradation trade-off) but with zero signal to the caller or
  operator that the image was ignored. Both now emit `slog.Warn` when a
  message carries `Parts` they can't translate. Also corrected the RFC's
  Gap 10 status, which listed Ollama alongside these two as an open gap —
  Ollama is a thin wrapper over the shared openaicompat adapter, which
  already forwards `Parts` correctly; that was stale. Covered by new
  tests in both adapter packages (Bedrock had no test file at all before
  this) and a targeted fuzz test on `Message`'s custom JSON polymorphism
  (`pkg/schema/content_test.go`, run for 2.5M+ executions with no crashes).
- Semantic cache false hits on session history (RFC Open Question #10,
  now resolved): `SessionMiddleware`-injected history dominated the
  token-overlap score enough that two unrelated questions in the same
  session could false-hit against each other. `SessionMiddleware` now
  attaches `historyLen` to the request context (`cache.WithHistoryLen`);
  `SemanticCache` embeds only the caller's actual new turn beyond it.
  `ExactCache` is unchanged — hashing the full message list including
  history is correct for exact-match. Fixing this first required merging
  the long-unmerged `semantic-cache` branch (Gap 2) into the mainline, since
  despite the RFC listing it as implemented it had never actually landed on
  `main`. Covered by both a cache-layer regression test and a new end-to-end
  `SessionMiddleware` → `CacheMiddleware` → `ChatHandler` integration test
  (previously, that composed chain had no test coverage at all); the
  end-to-end test was confirmed to fail without the fix before being
  restored to green.
- Budget enforcement (`plugins/budget`) had a TOCTOU race: `Intercept`
  checked `spent`, which is only updated by `OnEvent` — async, post-call,
  on the far side of a full LLM round trip. A burst of concurrent requests
  arriving while spend was still under the ceiling could all pass
  `Intercept` before any of their `OnEvent` accumulated real spend,
  overspending the ceiling by up to the burst size. `Intercept` now
  reserves each scope's running average cost per call synchronously before
  admitting the request; `OnEvent` releases the reservation and folds the
  real cost into that average regardless of outcome (a failed call still
  gives its reservation back). Self-calibrating from observed traffic, no
  new config — a scope's first-ever burst has no prior average to reserve
  against, which is a bounded cold-start gap rather than the previous
  unbounded one. Reproduced and fixed under `go test -race`.
- `chains:` requests ignored `stream:true` and always returned a plain JSON
  body instead of SSE: `chat.go` dispatched to `handleChain` before the
  `req.Stream` check ever ran. The terminal call in a chain (last
  sequential step, or the fusion judge) now streams when requested.

## [0.2.0] - 2026-07-24

### Added
- End-to-end SSE streaming for all 12 providers (`Stream` method on `provider.Provider`)
- JWT/API-key/mTLS auth middleware with hot-reload (`internal/ingress/auth.go`)
- Sliding-window rate limiter with per-tenant token tracking (`internal/domain/policy`)
- Circuit breaker state machine: Closed → Open → HalfOpen with configurable thresholds
- Retry + exponential backoff executor with 4xx-skip and context-cancel abort (`internal/domain/routing/executor.go`)
- Phase 2 scoring engine: policy-weighted filter → live `ModelPerfStore` stats → score → fallback chain (`internal/domain/scoring/engine.go`)
- Epsilon-greedy and UCB bandit algorithms with atomic regret tracking (`internal/domain/scoring/bandit.go`)
- Shadow routing: background goroutines per shadow model feeding bandit feedback (`internal/domain/scoring/shadow.go`)
- Telemetry plugin: `CallEvent` → `TelemetryRecord` + `ModelPerfStore` upsert + JSON Lines sink
- Config hot-reload via `fsnotify` with 200ms debounce; secrets expansion for Vault/AWS SSM
- Multi-tenant identity resolver with per-request context propagation
- OpenTelemetry tracing with OTLP HTTP export (no-op when `IXR_OTLP_ENDPOINT` unset)
- Prometheus metrics on `GET /metrics`
- `X-Request-ID` propagation middleware
- SHA-256 exact-match semantic cache with LRU eviction + TTL (`internal/domain/cache`)
- AWS Bedrock provider with raw SigV4 signing (no SDK dependency)
- Ollama, llama.cpp, and generic local model providers
- `POST /v1/embeddings` and `POST /v1/images/generations` with optional provider interfaces
- Full tool-calling spec: `Tool`, `FunctionDef`, `ToolChoiceObject` in `pkg/schema`
- Webhook fanout bus; NATS/Kafka/Kinesis/Pub/Sub compile stubs
- JSON Schema registry on `GET /v1/schema`; `api/proto/ixr.proto` for gRPC clients
- Redis/Postgres store interface stubs for `ModelPerfStore`, `PolicyStore`, circuit breaker state
- Tool/function calling wired through every configured adapter: OpenAI and
  Anthropic directly, Cerebras/DeepSeek/GitHub Models/Llama/Mistral/
  OpenRouter/SambaNova/Zhipu/Ollama/llama.cpp/local via the shared
  `openaicompat` adapter, and Gemini/Gemma via a dedicated translation for
  Gemini's `functionCall`/`functionResponse` shape. `pkg/schema` already had
  the types (`Tool`, `ToolCall`, `ToolChoice`) but no adapter forwarded them
  or parsed `tool_calls` back out of a response — confirmed live before the
  fix: a request with a tool defined against Anthropic came back "I don't
  have access to that tool." `Message` gains `ToolCallID`/`Name` so tool
  results (`role="tool"`) round-trip back to the originating call.
- Pricing table (`internal/domain/routing/pricing.go`) so budget enforcement
  actually prices real, by-name-requested models — the existing auto-routing
  catalog only priced 7 hardcoded candidate IDs that don't overlap with any
  model actually configured in `ixr.yaml` (e.g. `claude-haiku-4-5`,
  `llama-3.3-70b-versatile`), so cost silently came back $0 and spend caps
  never fired against live traffic.
- Model chaining (`chains:` config in `ixr.yaml`): a request naming a chain
  instead of a model runs a fixed sequence of models, each step's reply
  feeding the next step's prompt (`internal/domain/chain`,
  `internal/ingress/chain.go`). Restores the `fast-refine`/`smart-qa`/
  `debate` example chains in `demo-ixr.yaml`.
- Multimodal input (vision): `Message.Parts` carries image content alongside
  text (`pkg/schema/content.go`), additive to the existing `Content` string
  so text-only callers see no change. Wired through OpenAI, Anthropic
  (`data:` URI and URL image sources), and `openaicompat`.
- Bandit-driven exploration in primary `model:"auto"` routing, opt-in via
  `IXR_AUTO_BANDIT=true` (default off): `scoring.Engine.SetBandit` lets
  `Decide` pick via the existing epsilon-greedy bandit instead of always
  taking the top deterministic score; `plugins/banditreward` closes the loop
  by training the bandit from real primary-routed traffic, sharing arm
  statistics with shadow routing.
- Published `schema/ixr.proto` (Protocol Buffers v3) and `schema/ixr.schema.json`
  (JSON Schema draft 2020-12) covering `CallEvent`, `RequestEnvelope`,
  `ResponseEnvelope`, `Message`, and related types, so non-Go consumers can
  generate typed bindings or validate payloads without reverse-engineering
  the event stream (`schema/README.md`)
- Hierarchical budget enforcement plugin: spend accumulates and is gated at
  org → team → user scope (`tenantID[:teamID[:userID]]`), configured via
  `tenants.<id>.quotas` / `tenants.<id>.teams.<id>.quotas` in `ixr.yaml`
  (`plugins/budget`, `pkg/guardrail`)
- `internal/domain/cost.ForUsage` prices a call against the routing catalog
  and populates `CallEvent.Cost` on every request path (previously always
  zero, so budget enforcement never actually triggered against live traffic)
- `CallEvent` gains `TeamID`/`UserID` fields (from identity context) so spend
  can be attributed below the tenant level
- User memory: facts extracted from conversation turns (`RuleExtractor`,
  regex-based) are stored per user (`tenantID:userID`) and injected as
  context into new requests, gated on `IXR_MEMORY=true` and a concrete
  `X-IXR-UserID` (`internal/domain/memory`, `plugins/memory`,
  `internal/ingress/memory_middleware.go`)
- Memory storage is bounded rather than growing forever: entries expire
  after a TTL (`IXR_MEMORY_TTL_SEC`, default 1h) and are capped per user
  (`IXR_MEMORY_MAX_PER_USER`, default 50); the on-disk journal is
  recompacted on startup and periodically at runtime
  (`IXR_MEMORY_COMPACT_INTERVAL_SEC`, default 15m)

### Fixed
- Provider entries in `ixr.yaml` with empty `api_key` are now silently skipped instead of failing startup
- Streaming was broken for every request, not just rate-limited ones:
  `internal/ingress/ratelimit.go`'s `responseCapture` wrapped every response
  in a struct that didn't forward `http.Flusher`, and rate-limit middleware
  sits unconditionally in the request chain. `responseCapture` now forwards
  `Flush()`.
- User memory silently no-oped for the default tenant: both the read and
  write paths treated `tenant_id == "default"` as anonymous, disabling
  memory for any single-tenant/no-auth deployment — the demo config's own
  setup. `"default"` is a legitimate tenant (the identity resolver's
  default), not an anonymity signal.
- Prometheus metrics were registered at startup but `Metrics.Record()` had
  no callers, so `/metrics` reported nothing after real traffic. Wired into
  both the streaming and non-streaming chat paths, and into chain step
  execution.
- `CallEvent.Latency` was `time.Duration` tagged `json:"latency_ms"` —
  `time.Duration` has no custom `MarshalJSON`, so it serialized as raw
  nanoseconds under a field name that lied about units. New `EventLatency`
  type marshals as milliseconds.
- Empty/missing `messages` were forwarded to the provider and its rejection
  reported back as a misleading `502 provider_error`; now validated at
  ingress and returned as `400`.
- `routing.Execute`/`ExecuteStream` (retry with backoff, fallback-chain
  walking, context-length escalation) existed and were exposed as
  `ChatOption`s but had zero call sites outside their own file — every
  direct-model request got exactly one attempt, and the circuit breaker was
  only ever consulted for `model:"auto"` candidate filtering. Both the
  streaming and non-streaming chat paths, and each chain step, now go
  through the executor and check/update the circuit breaker. Also fixed a
  latent correctness bug found while wiring this in: `streamWithRetry`
  retried unconditionally on any non-4xx error, including mid-stream
  failures after chunks were already flushed to the client, which would
  have duplicated output on the wire — it now stops retrying once any chunk
  has been emitted.
- `internal/domain/routing/router.go` failed to compile — `knownContextWindows`
  and `defaultContextWindow` were each declared twice, with a stray
  struct-literal fragment sitting outside any struct in between, from an
  earlier PR merge that silently interleaved two branches which both added
  the same `ContextWindow` infrastructure independently

## [0.1.0] - 2026-05-08

### Added
- `pkg/ixr` — `Start(opts ...Option) error` one-line entry point; `WithPort`, `WithConfigFile` options
- `pkg/schema` — `CallEvent`, `RequestEnvelope`, `ResponseEnvelope`, `Message`, `Choice`, `Usage`, `CostBreakdown`, `ToolCall` public types
- `pkg/plugin` — `EventConsumer` interface for zero-fork extensibility
- `pkg/provider` — `Provider` interface (`Name`, `Chat`)
- `pkg/bus` — `Bus` interface (`Publish`, `Subscribe`)
- OpenAI provider — full `POST /v1/chat/completions` passthrough, OpenAI-compatible response shape
- Anthropic provider — Messages API integration, system-message lifting, stop-reason normalisation
- Model-prefix router — `gpt-*` / `o1` / `o3` → OpenAI; `claude-*` → Anthropic
- In-memory event bus — buffered channel, non-blocking publish, panic-safe plugin dispatch
- Plugin manager — registers `EventConsumer` plugins at startup
- Audit-log plugin — emits every `CallEvent` as a JSON line to stdout
- Config loader — `ixr.yaml` with `${ENV_VAR}` interpolation, auto-discovery, env-var override
- `cmd/ixr` binary — `--config` and `--port` flags
- `Dockerfile` — multi-stage scratch image, `linux/amd64` + `linux/arm64`
- Table-driven tests — translators, adapters, chat handler, config loader (no live API keys required)
- GitHub Actions — test (go vet + staticcheck + race detector), release (multi-arch binaries, cosign, syft SBOM, ghcr.io image), govulncheck
- Apache 2.0 license

[0.2.0]: https://github.com/YashVishwas/ixr/releases/tag/v0.2.0
[0.1.0]: https://github.com/YashVishwas/ixr/releases/tag/v0.1.0
