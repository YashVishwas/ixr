# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- `internal/domain/routing/router.go` failed to compile — `knownContextWindows`
  and `defaultContextWindow` were each declared twice, with a stray
  struct-literal fragment sitting outside any struct in between, from an
  earlier PR merge that silently interleaved two branches which both added
  the same `ContextWindow` infrastructure independently

## [0.2.0] - 2026-06-05

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

### Fixed
- Provider entries in `ixr.yaml` with empty `api_key` are now silently skipped instead of failing startup

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

[0.1.0]: https://github.com/YashVishwas/ixr/releases/tag/v0.1.0
