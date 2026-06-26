# RFC 0001 — ixr: Adaptive AI Gateway

**Status:** in progress
**Authors:** ixr core team
**Last updated:** 2026-06-26

---

## Executive Summary

ixr is a small, fast, embeddable inference proxy written in Go. It sits between every LLM call a service makes and the upstream provider, making those calls schema-aware, observable, and intelligently routed — without the calling service changing anything beyond a base URL.

The bet is straightforward: the LLM gateway space has consolidated around tools that are either too heavy to operate (LiteLLM, Portkey), too shallow to extend (Helicone), or cloud-locked on the features that matter (Portkey's semantic cache, guardrails, prompt versioning). ixr is the gateway you would build if you started today knowing what each of those products got wrong.

This document covers the full project scope: the design constraints that govern every decision, what has already been built, the remaining feature gaps and their planned designs, the plugin architecture that unifies the extension model, and the boundary of what ixr explicitly will not absorb.

---

## Why Existing Tools Fail

| Tool | Primary failure |
|---|---|
| LiteLLM | Supply chain compromise (March 2026); 16 CVEs across 2024–2026; Python runtime overhead; requires PostgreSQL + Redis to operate |
| Helicone | Acquired by Mintlify, entering maintenance mode; routing is shallow; primarily an observability layer |
| Portkey | Meaningful features (semantic caching, guardrails, prompt versioning) are cloud-hosted SaaS only; the OSS version is not viable for on-premise deployments |
| Kong AI Gateway | Heavy infra footprint; not embeddable; designed for API management, not LLM-specific routing intelligence |

The common thread: every existing gateway either cannot be trusted operationally, cannot be run self-hosted with full features, or cannot be inserted without significant infrastructure changes.

ixr's differentiation is not a feature list. It is the combination of: **single binary, no external dependencies, embeddable as a Go library, fully self-hosted, and designed from the start to route intelligently rather than just passthrough with logging.**

---

## Core Design Constraints

These constraints are not preferences — they are the invariants that every feature decision is evaluated against. Violating any of them requires an explicit ADR.

**1. Single binary, no mandatory external dependencies.**
ixr must run with `./ixr --config ixr.yaml`. No sidecar, no database, no message broker required to get started. External services (Redis, pgvector, NATS) can be used as optional backends behind existing interfaces, but they are never mandatory.

**2. Embeddable as a Go library.**
`import "github.com/YashVishwas/ixr/pkg/ixr"; go ixr.Start()` is a first-class path. Features that require a separate process or a network hop to activate are not eligible for the core.

**3. Surgical changes only.**
Features are added by extending existing interfaces or adding new optional components, not by modifying the request path or rearchitecting existing behaviour. Every feature must degrade gracefully when not configured — the unconfigured state must be equivalent to the pre-feature state.

**4. No data leaves the process without explicit operator configuration.**
The built-in implementations of every feature (embedder, vector store, guardrails) run in-process with no external calls. Operators who want higher-quality cloud-backed variants can configure them; the default must never make a network call beyond the configured LLM providers.

**5. The request path latency budget is untouchable.**
Features that run in the request path (Lookup, pre-call interceptors) have hard time budgets enforced in code. A misconfigured or slow component must degrade gracefully to a miss/pass-through, never to a hang or timeout that affects the caller.

**6. The plugin interface is the extension point.**
New behaviour is added as plugins that consume `CallEvent` from the bus (async, post-call) or as request interceptors (sync, pre-call). Modifying `internal/ingress/` or `internal/domain/` for a feature that could be a plugin is the wrong abstraction.

---

## What Is Already Built

| Capability | Location | Notes |
|---|---|---|
| Streaming (SSE) | `internal/ingress/stream.go` | Full server-sent events |
| Auth — API keys, JWT, mTLS | `internal/ingress/auth.go` | All three ingress auth modes |
| Rate limiting (per-identity, sliding window) | `internal/ingress/ratelimit.go` | Returns 429 + Retry-After |
| Circuit breaker | `internal/domain/circuitbreaker/` | Per-model, shared state |
| Routing + fallback chains | `internal/domain/routing/` | Router, scorer, fallback, filter |
| Exact-match response cache | `internal/domain/cache/cache.go` | SHA-256 keyed |
| Semantic response cache | `internal/domain/cache/semantic*.go` | Two-layer; file-journal persistence |
| Multi-tenancy + per-tenant credentials | `internal/domain/tenant/` | Per-tenant rate limits and provider keys |
| Intent parsing | `internal/domain/intent/` | Taxonomy + parser + complexity scoring |
| Bandit scoring engine | `internal/domain/scoring/` | Epsilon-greedy/UCB, reward, regret tracking |
| Shadow routing | `internal/domain/scoring/shadow.go` | Parallel shadow requests, offline comparison |
| Signed releases + SBOM | `.github/workflows/release.yml` | cosign keyless signing + syft SPDX |
| Vulnerability scanning CI | `.github/workflows/govulncheck.yml` | govulncheck on every push |
| 16 provider adapters | `internal/adapters/providers/` | OpenAI, Anthropic, Bedrock, Mistral, Ollama, DeepSeek, LlamaCpp, Llama, Cerebras, GitHub Models, Google AI, OpenRouter, SambaNova, ZhiPu, OpenAI-compat, Local |
| Typed event bus | `pkg/bus/` | In-memory channel; external adapters planned |
| Schema — typed CallEvent | `pkg/schema/` | event, cost, identity, telemetry, tool, audio, images, embeddings |
| Plugins | `plugins/` | audit-log, telemetry, token-usage |
| One-line embed + single binary + Docker | `pkg/ixr/`, `cmd/ixr/`, `Dockerfile` | All three insertion paths |

---

## Feature Gap Roadmap

The following gaps are ordered by the combination of operator impact and implementation scope. Each gap includes a design sketch anchored to the existing codebase so the approach is unambiguous before implementation begins.

---

### Gap 1 — PII Detection / Guardrails Plugin

**What it is:** A synchronous pre-call interceptor that scans outbound prompts before they reach a provider and blocks or redacts requests containing PII (names, emails, phone numbers, credit card numbers, SSNs, health data).

**Why it matters:** Teams in regulated industries (finance, healthcare, legal) cannot accept that customer data may leave their infrastructure in a prompt. Portkey's guardrails run in Portkey's cloud — the scan itself happens outside the customer's boundary, defeating the purpose. LiteLLM has no content scanning in the request path at all. ixr's self-hosted model makes it the only viable option for compliance-constrained deployments.

**Design sketch:**

The current plugin interface is post-call and async:

```go
// pkg/bus/bus.go
type EventConsumer interface {
    Name() string
    OnEvent(ctx context.Context, ev *schema.CallEvent) error
}
```

PII guardrails require a synchronous pre-call interface. A new interface needs to be defined and wired into the ingress layer:

```go
// internal/domain/guardrail/guardrail.go
type RequestInterceptor interface {
    Name() string
    // Intercept inspects the request before it is sent to a provider.
    // Return a non-nil error to block the request; the error message becomes the 403 body.
    Intercept(ctx context.Context, req *schema.RequestEnvelope) error
}
```

`internal/ingress/` would chain interceptors before the provider call, parallel to the existing middleware stack. A reference `PIIGuardrail` implementation in `plugins/pii-guardrail/` would use compiled regex patterns for common PII categories (LUHN-validated card numbers, US SSN format, RFC 5322 email, E.164 phone). Redaction mode replaces matches with `[REDACTED:TYPE]`; block mode returns a 403 with the category that triggered.

The interceptor chain is opt-in and ordered. A nil/unconfigured chain is a no-op with zero overhead.

---

### Gap 2 — Semantic Cache Backend

**Status: implemented** (`semantic-cache` branch, committed `c5bbb77`)

See [Semantic Cache — Detailed Design](#semantic-cache--detailed-design) for the full design. Summary:

- Two-layer: exact-match (SHA-256) first, cosine-similarity scan second
- Built-in `WordVectorizer` is pure Go, sub-millisecond, zero external deps
- `PersistentSemanticBackend` journals entries to a file; replays on startup
- Lookup embed step is capped at 5ms — a slow embedder degrades to a miss, never a hang
- Enabled via `IXR_SEMANTIC_CACHE=true`; persistence via `IXR_CACHE_DIR=/data`

---

### Gap 3 — Budget Enforcement Plugin

**What it is:** A plugin that accumulates spend per identity from `CallEvent.Cost` (already on the bus) and enforces a hard ceiling — blocking further calls when the budget is exhausted. Emits a warning event at a configurable threshold (e.g. 80%).

**Why it matters:** Runaway LLM spend is one of the most common production incidents. A single misconfigured prompt loop or unexpected traffic spike can generate thousands of dollars in API costs before anyone notices. Hard spend limits that block calls — not just alert after the fact — are the only reliable protection. No existing OSS gateway makes this first-class and self-hosted.

**Design sketch:**

Budget enforcement requires both a post-call accumulator (to track spend) and a pre-call gate (to block over-budget identities). This means the plugin implements both `EventConsumer` (existing) and `RequestInterceptor` (Gap 1):

```go
type BudgetPlugin struct {
    mu      sync.Mutex
    spent   map[string]float64  // identity → USD spent
    limits  map[string]float64  // identity → USD limit
    warnAt  float64             // fraction of limit at which to warn (e.g. 0.8)
}

// EventConsumer — accumulates spend post-call.
func (b *BudgetPlugin) OnEvent(ctx context.Context, ev *schema.CallEvent) error { ... }

// RequestInterceptor — blocks the call if identity is over budget.
func (b *BudgetPlugin) Intercept(ctx context.Context, req *schema.RequestEnvelope) error { ... }
```

Spend state is in-memory. Persistence follows the same file-journal pattern as the semantic cache — appending spend records to disk, replaying on startup — so budgets survive restarts without requiring a database. Limits are loaded from config or the policy store.

---

### Gap 4 — Schema Definition for Non-Go Consumers

**What it is:** A published `.proto` file (and generated JSON Schema) for the core `pkg/schema` types — `CallEvent`, `RequestEnvelope`, `ResponseEnvelope`, `Message` — so engineers building plugins or consuming the event bus in Python, TypeScript, Rust, or any other language have a formal contract.

**Why it matters:** Without a schema definition, a non-Go engineer who wants to consume ixr's event stream has to reverse-engineer the JSON output from the audit-log plugin. This limits the plugin ecosystem to Go engineers and creates an invisible dependency on the internal struct layout.

**Design sketch:**

`pkg/schema/schema.proto` generated from the existing Go structs. The Go types are the source of truth; the proto is derived and committed. A `make gen-schema` target runs `protoc` with `protoc-gen-go` and also emits a JSON Schema for non-protobuf consumers.

The proto is versioned at `v1`. Breaking changes to `pkg/schema` require a proto version bump. This is the same stability contract that applies to the Go `pkg/` boundary.

No runtime dependency on proto — the `.proto` file and generated stubs are publish artefacts, not core dependencies. The Go types remain plain structs with JSON tags.

---

### Gap 5 — Context Window Overflow → Automatic Model Escalation

**What it is:** When a provider returns a context-length error, the routing layer automatically escalates the request to the next model in the fallback chain that has a sufficient context window, rather than surfacing the error to the caller.

**Why it matters:** Context overflow errors surface as cryptic 400s that every calling service handles individually. Every team building with LLMs has written some version of "if context too long, retry on bigger model." That logic belongs in the gateway. The fallback chain already exists — the addition is recognising a context-length error as a routing signal rather than a terminal failure.

**Design sketch:**

Provider adapters already return typed errors for provider-specific conditions. A new sentinel needs to be added:

```go
// internal/domain/routing/errors.go
type ContextLengthError struct {
    Model         string
    RequestTokens int
    ModelLimit    int
}
func (e *ContextLengthError) Error() string { ... }
```

Each provider adapter maps its provider-specific context-length error code (OpenAI: `context_length_exceeded`; Anthropic: `400` with `max_tokens` in body) to `*ContextLengthError`.

The executor in `internal/ingress/chat_handler.go` already walks the fallback chain on errors. The change is: when the error is `*ContextLengthError`, skip to the first fallback model whose `ContextWindow > RequestTokens` rather than the next model by score. The model catalog (`internal/domain/routing/catalog.go`) gains a `ContextWindow int` field per entry.

No change to the caller-facing API. The escalation is transparent.

---

### Gap 6 — Reasoning Model Token Budget Awareness

**What it is:** Provider-adapter-level awareness that certain models (o3, Gemini thinking variants) consume a large portion of `max_tokens` on internal reasoning before producing visible output. The adapter adjusts the effective token budget so the caller reliably gets the output length they requested.

**Why it matters:** Developers requesting 1,000 output tokens from o3 may receive 37 visible tokens — the remaining 963 were consumed by internal chain-of-thought that the model counts against `max_tokens`. No existing gateway accounts for this at the adapter layer. Callers over-provision token limits to compensate, increasing cost.

**Design sketch:**

Each provider adapter knows which model families it serves. A new field on the adapter (or a per-model config map) holds the reasoning overhead ratio:

```go
// internal/adapters/providers/openai/openai.go
var reasoningOverheadRatio = map[string]float64{
    "o3":      0.92,  // ~92% of tokens consumed by reasoning
    "o3-mini": 0.85,
}
```

Before forwarding the request, the adapter checks if the model is in the map. If so, it scales up `max_tokens` by `1 / (1 - ratio)` — so a caller requesting 1,000 visible tokens gets `1,000 / 0.08 = 12,500` passed to the provider. The visible output is still ~1,000 tokens; the cost is higher but predictable and declared.

This is opt-in per model family and configurable via the provider config block. The ratio values are conservative defaults that operators can tune.

---

### Gap 7 — OTEL-Native Span Emission

**What it is:** The telemetry plugin (`plugins/telemetry/`) emitting proper OpenTelemetry spans using the GenAI semantic conventions (`gen_ai.system`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.response.model`, etc.) so teams can send LLM traces directly to any OTLP-compatible backend.

**Why it matters:** Teams already running OpenTelemetry for their services expect LLM calls to appear as spans in the same trace. The GenAI semantic conventions are stabilising as the standard for this. All the data is already in `CallEvent` — this is a matter of emitting it in the right format. LiteLLM's OTEL support requires PostgreSQL + Redis + the LiteLLM UI; ixr should emit directly to any OTLP endpoint with no intermediate store.

**Design sketch:**

The tracer is already initialised in `pkg/ixr/ixr.go` via `observability.InitTracer`. The telemetry plugin currently emits JSON lines to stderr. The extension adds an optional OTLP exporter path:

```go
// plugins/telemetry/otel.go
func (p *Plugin) emitSpan(ctx context.Context, ev *schema.CallEvent) {
    _, span := tracer.Start(ctx, "gen_ai.chat",
        trace.WithAttributes(
            attribute.String("gen_ai.system",               ev.Provider),
            attribute.String("gen_ai.request.model",        ev.Model),
            attribute.Int("gen_ai.usage.input_tokens",      ev.Usage.InputTokens),
            attribute.Int("gen_ai.usage.output_tokens",     ev.Usage.OutputTokens),
            attribute.Float64("gen_ai.usage.cost_usd",      ev.Cost.TotalUSD),
            attribute.Bool("gen_ai.response.cached",        ev.CacheHit),
        ),
    )
    defer span.End()
}
```

Enabled when `IXR_OTLP_ENDPOINT` is set (already wired in `ixr.go`). The plugin checks whether a tracer is active and emits spans only if it is. No OTEL dependency is added to the core — it is already a dependency of `internal/observability/`.

---

### Gap 8 — Hierarchical Budget Controls

**What it is:** An extension of Gap 3 (budget enforcement) to support nested spend ceilings: org → team → user. A request that would push any level over its ceiling is blocked, regardless of whether the individual key's limit has been reached.

**Why it matters:** Enterprise teams need to guarantee total org spend regardless of how individual teams consume their sub-limits. A team staying within its own budget can still blow an org-level ceiling if the hierarchy is not enforced end-to-end. This is the difference between a tool used by a developer and a tool trusted by a finance team.

**Design sketch:**

`internal/domain/tenant/` and `internal/domain/identity/` already provide the data model. The budget plugin from Gap 3 extends its `Intercept` logic to walk up the tenant tree:

```go
func (b *BudgetPlugin) Intercept(ctx context.Context, req *schema.RequestEnvelope) error {
    id := identity.FromContext(ctx)
    for _, scope := range []string{id.Key, id.Team, id.Org} {
        if scope == "" {
            continue
        }
        if b.isOverBudget(scope) {
            return fmt.Errorf("budget exceeded for %s", scope)
        }
    }
    return nil
}
```

Limits are configurable per scope level. Spend accumulated by `OnEvent` is keyed by scope so org-level spend is the sum of all team-level spend within that org. No new data model is needed — the tenant hierarchy already exists.

---

## Plugin Architecture as the Extension Model

The gaps above split into two categories by when they need to run:

**Post-call, async (EventConsumer):** budget accumulation (Gap 3), OTEL span emission (Gap 7), token-usage tracking, audit logging. These subscribe to `CallEvent` from the bus. The bus is non-blocking — a slow plugin never affects request latency.

**Pre-call, sync (RequestInterceptor):** PII guardrails (Gap 1), budget gate (Gap 3), hierarchical budget gate (Gap 8). These run in the request path and must be fast. They return an error to block, nil to pass through.

The `RequestInterceptor` interface (Gap 1) is the only new abstraction needed to address five of the seven remaining gaps. Everything else is either an existing plugin type or a change internal to a provider adapter.

The ingress pipeline with both interfaces wired looks like:

```
POST /v1/chat/completions
        ↓
   auth + rate limit middleware
        ↓
   [RequestInterceptor chain]  ← PII guardrail, budget gate  (sync, pre-call)
        ↓
   cache lookup
        ↓
   scoring engine → provider call
        ↓
   response to caller
        ↓
   cache store (post-response)
        ↓
   [EventConsumer bus]  ← budget accumulate, OTEL span, audit-log, telemetry  (async, post-call)
```

A plugin can implement one or both interfaces. The plugin manager registers it in the appropriate chain at startup.

---

## Semantic Cache — Detailed Design

*(Gap 2 — implemented. This section serves as the reference for how a gap moves from design to implementation.)*

### Two-layer lookup

```
Request
  │
  ▼
ExactCache.Lookup(req)       ← O(1), SHA-256 hash, no embedding cost
  │ hit → return cached response
  │ miss
  ▼
WordVectorizer.Embed(text)   ← sub-ms, pure Go, no external call
  [capped at 5ms — degrades to miss if exceeded]
  │
  ▼
MemorySemanticBackend.Find   ← O(n) cosine scan, ~0.1ms for 1k entries
  │ hit  → return cached response
  │ miss → call upstream LLM
  ▼
upstream LLM call
  │
  ▼
ExactCache.Store + SemanticBackend.Store  ← after response is written to client
```

### Components

**`internal/domain/cache/semantic.go`** — `Embedder` and `SemanticBackend` interfaces; `MemorySemanticBackend` (in-process cosine store); `PersistentSemanticBackend` (file-journal wrapper).

**`internal/domain/cache/vectorize.go`** — `WordVectorizer`: FNV-32a token hashing into a 512-dim float32 vector, L2-normalised. Deterministic. Zero dependencies.

**`internal/domain/cache/semantic_cache.go`** — `RequestAwareCache` interface; `ExactCache` (SHA-256 wrapper); `SemanticCache` (two-layer orchestrator).

### Latency protection

The embed step in `SemanticCache.Lookup` runs under `context.WithTimeout(ctx, 5ms)`. A slow or remote embedder degrades to a miss — never a hang. `Store` is called after the response has been written to the client; no embedding work is on the critical path.

### Persistence without an external service

`PersistentSemanticBackend` appends each stored entry as a JSON line to `dir/semantic.jsonl`. On startup, `replayJournal` reads the file, skips expired entries, and loads the rest into `MemorySemanticBackend`. No database. No sidecar. Mount a volume at `IXR_CACHE_DIR` for cross-restart durability; leave it unset for ephemeral in-memory operation.

### Configuration

| Env var | Default | Effect |
|---|---|---|
| `IXR_SEMANTIC_CACHE` | *(unset)* | Set to `true` to enable |
| `IXR_CACHE_DIR` | *(unset)* | Journal directory; empty = in-memory only |
| `IXR_CACHE_SIZE` | `1024` | Max entries |
| `IXR_CACHE_TTL_SEC` | `300` | Entry TTL in seconds |

### Similarity threshold

Default: **0.92**. Near-duplicates with minor wording changes score 0.93–0.99 with `WordVectorizer`. Unrelated prompts score 0.1–0.4. The gap is wide; the threshold is robust to normal variation in phrasing.

`WordVectorizer` measures token overlap, not semantic meaning. For paraphrase-level matching ("summarize" ≈ "give me a summary"), a provider-backed embedder is needed (see Open Questions).

### Drawbacks

- `WordVectorizer` is token overlap, not semantics. Paraphrase misses are expected.
- O(n) scan becomes slow above ~10k entries (~5ms at 10k, competing with the Lookup timeout).
- Journal is append-only; expired entries accumulate until next restart.
- No cross-instance sharing — each process warms its own cache independently.

### Alternatives considered

pgvector, Weaviate, Qdrant — richer indexing and cross-instance sharing, but require an external service. Rejected for the initial implementation; can be added as a `SemanticBackend` implementation behind the existing interface.

Provider-backed embedder on Lookup — better semantic quality, but a network call on every cache miss is worse than no cache at all. Acceptable on the Store path only.

---

## Non-Goals

These are real problems in the ecosystem. ixr will not absorb them.

**Model drift detection / output quality scoring.** Evaluating whether a model's outputs are degrading requires an eval framework — semantic correctness, factual accuracy, human preference signals. That is a different product category (Langfuse, Braintrust). ixr emits `CallEvent` with the data; eval tools consume it.

**Multi-agent observability.** Tracking sub-agent calls, task trees, and memory reads across an agent system requires conventions that are not yet stable (the OpenTelemetry GenAI SIG is still defining them). Adding this now means owning a moving target with no clear boundary.

**Context rot mitigation.** Quality degrades as context grows. Fixing it requires chunking, summarisation, or retrieval strategies at the application layer. ixr can route around context overflow (Gap 5) but cannot fix the underlying problem.

**Prompt versioning / A/B testing.** Useful, but it is a developer workflow tool — closer to a feature flag system than a routing layer. Adding it would pull ixr toward being a prompt management platform.

**Multi-tenancy UI / admin console.** Config, tenant management, and spend dashboards are operator tooling. ixr exposes the data (metrics endpoint, OTLP spans, audit-log plugin) and lets operators build or choose their own dashboard.

---

## Interface Stability

**`pkg/` is frozen at v1.** The public API — `pkg/ixr`, `pkg/bus`, `pkg/schema`, `pkg/provider` — follows semver. Breaking changes require a major version bump. Third-party plugins and embedding services depend on these.

**`internal/` is fair game.** Anything under `internal/` can change between releases without notice. Plugins that import `internal/` directly are unsupported.

**Plugin interfaces (`EventConsumer`, `RequestInterceptor`) are stable once shipped.** Adding a method to either interface is a breaking change. New capabilities are added via new optional interfaces (checked with a type assertion at registration time), not by modifying the existing ones.

**Provider adapters implement `pkg/provider.Provider`.** That interface is frozen. New providers are self-contained additions; changing the interface requires a major version.

---

## Open Questions

1. **`RequestInterceptor` placement.** Should it live in `pkg/` (public, versioned) or `internal/domain/guardrail/` (internal, flexible)? If external plugins need to implement it, it must be `pkg/`. Decision needed before Gap 1 implementation.

2. **Separate embedder for Store vs Lookup.** `SemanticCache` currently uses one `Embedder` for both paths. A provider-backed embedder on Store (better quality) with `WordVectorizer` on Lookup (zero latency) would improve hit rates without affecting the request path. The struct can accept two embedders; `NewSemanticCache` signature change needed.

3. **`IXR_CACHE_THRESHOLD` env var.** The cosine similarity threshold is hardcoded at 0.92. Should be operator-configurable before the semantic cache is considered stable.

4. **Journal compaction.** For processes running for weeks, the journal accumulates expired entries. A startup compaction pass (rewrite file without expired entries) is straightforward to add. Low priority until someone hits it in practice.

5. **Budget persistence format.** Gap 3 (budget enforcement) uses a similar file-journal pattern to the semantic cache. Should the two share a persistence abstraction, or remain independent implementations? Sharing reduces code but couples unrelated features.

6. **Cross-instance cache.** A shared `SemanticBackend` (pgvector) behind the existing interface enables cache sharing across scaled deployments. The `SemanticBackend` interface already supports this — it is purely an implementation choice. When to add it depends on whether ixr targets single-instance or multi-instance deployments as the primary case.

---

## Future Work

- **`OllamaEmbedder`** — `Embedder` backed by Ollama's `/api/embeddings` for paraphrase-level matching on the Store path.
- **HNSW in-memory index** — replace O(n) scan with approximate nearest-neighbour for deployments with >10k cached entries.
- **`PgvectorBackend`** — `SemanticBackend` backed by pgvector for cross-instance cache sharing.
- **External bus adapters** — NATS, Kafka, Kinesis, Google Pub/Sub behind `pkg/bus.Bus`.
- **Secrets rotation without restart** — Vault, AWS Secrets Manager, GCP Secret Manager for provider credentials.
- **`model: "auto"` as default** — when no model is specified, the scoring engine picks. Currently requires explicit opt-in.
- **Quality score in reward function** — the bandit scoring engine has a placeholder `δ * quality_score` term; wiring it requires a lightweight output quality signal (e.g. response length, format adherence, downstream error rate).
- **CNCF Sandbox submission** — the long-term governance target once the project reaches production stability across multiple adopters.
