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
| Session continuity | `internal/domain/session/`, `internal/ingress/session_middleware.go` | Cross-request history; delta journal; `X-IXR-Session-ID` header |
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

**Status: implemented, but found unwired during stress-testing.** `routing.Execute`/`ExecuteStream`, `IsContextLengthError`, and `escalateCandidates` were fully built and unit-tested, but `internal/ingress/chat.go` called providers directly (`p.Chat`/`p.Stream`) and never invoked `Execute` at all for a request naming a model directly — identical in nature to the budget-cost wiring bug found earlier via dogfooding. A real context-length overflow returned a raw 502 instead of escalating, for both `model:"auto"` (whose `FallbackChain`, computed by `scoring.Engine.Decide`, was discarded — `chat.go` only read `decision.Model`) and any explicit model. Fixed: `chat.go` now computes a `fallbackChain` up front — from the scoring engine's decision for `auto`, or via new `routing.FallbackChainFor` for an explicit model that's a catalog entry — and routes through `Execute`/`ExecuteStream` whenever one is available. An explicit model outside the catalog (the common case) keeps the pre-existing direct-call/502 behavior, since there's no `ContextWindow` data to escalate against and escalating to unconfigured fallback candidates would just retry-storm. `schema.CallEvent` gained `FallbackUsed`/`FallbackFrom`, threaded through to `plugins/telemetry`. Two related executor bugs were also caught and fixed while wiring this up: `FallbackUsed` was computed from a loop index that resets to 0 after an escalation (so an escalated response wrongly reported `FallbackUsed=false`), and a fully-exhausted fallback chain returned a zero-value `Model`/`Provider`, so failure logging misattributed the error to the original primary instead of whichever candidate actually produced it. Covered by `TestChatHandler_ContextLengthEscalation_ExplicitCatalogModel`, `_AutoMode`, and `_NonCatalogModelDoesNotEscalate`.

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

### Gap 9 — User Memory & Cross-Session Context

**What it is:** A user-level memory store that extracts facts, preferences, and context from conversation history, persists them keyed by user identity, and injects relevant memories into new sessions via the system prompt — giving the gateway a persistent model of each user across all their conversations.

**Why it matters:** Session continuity (Gap 9's prerequisite, implemented on `feat/session-continuity`) keeps a single conversation thread alive across HTTP requests. But when that session ends and a new one starts, everything is forgotten. Users have to re-establish their context every time — their name, preferences, what they were working on. ChatGPT's memory feature demonstrates that users expect AI infrastructure to remember them. For enterprise deployments, this is the difference between a stateless proxy and an intelligent assistant layer that compounds value over time.

The three questions this closes:
1. *Are messages stored permanently?* — Yes, as distilled memories, not raw transcripts.
2. *Does ixr build a profile around me?* — Yes, `UserMemoryStore` accumulates facts per user identity.
3. *Can I use context from a previous session in a new one?* — Yes, relevant memories are injected at the start of each new session.

**Design sketch:**

Three components, each building on existing infrastructure:

**1. `internal/domain/memory/memory.go` — `UserMemoryStore`**

```go
// MemoryEntry is one extracted fact about a user.
type MemoryEntry struct {
    ID        string    `json:"id"`
    UserKey   string    `json:"user_key"`   // tenantID:userID
    Content   string    `json:"content"`    // "User's name is Arun"
    Tags      []string  `json:"tags"`       // ["name", "identity"]
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type UserMemoryStore interface {
    // Store saves an extracted memory for a user.
    Store(ctx context.Context, entry MemoryEntry) error
    // Retrieve returns the N most relevant memories for the given context text.
    // Uses semantic similarity so "what's my name?" retrieves name-related memories.
    Retrieve(ctx context.Context, userKey string, contextText string, topN int) ([]MemoryEntry, error)
    // List returns all memories for a user (for inspection/deletion).
    List(ctx context.Context, userKey string) ([]MemoryEntry, error)
    // Delete removes a specific memory by ID.
    Delete(ctx context.Context, id string) error
}
```

The `Retrieve` method uses the existing `SemanticBackend` interface from `internal/domain/cache/semantic.go` — memories are stored as embeddings alongside their text, and retrieval is a cosine similarity scan over the user's memory pool. This is the same `WordVectorizer` + `MemorySemanticBackend` infrastructure already built, scoped per user rather than per request.

**2. `internal/domain/memory/extractor.go` — `MemoryExtractor`**

```go
type MemoryExtractor interface {
    // Extract derives zero or more memory entries from a completed conversation turn.
    Extract(ctx context.Context, userKey string, turn ConversationTurn) ([]MemoryEntry, error)
}

type ConversationTurn struct {
    UserMessage      string
    AssistantMessage string
    SessionID        string
}
```

Two implementations:

- **`RuleExtractor`** — regex-based patterns for high-confidence facts: name introductions ("my name is", "I'm called"), stated preferences ("I prefer", "I always", "I usually"), explicit facts ("I work at", "I live in", "I'm a"). Zero LLM calls, sub-millisecond. High precision, limited recall.

- **`LLMExtractor`** — sends the turn to a cheap, fast model (e.g. `gpt-4o-mini`) with a structured prompt asking it to extract memorable facts as a JSON array. Higher recall, costs one small LLM call per turn. The call is made asynchronously post-response so it never adds latency.

**3. Injection point — `SessionMiddleware` extension**

The `SessionMiddleware` already runs at the start of each request. When a user memory store is configured, it adds one step before injecting session history:

```
1. Load session history (existing)
2. If session is new (no history loaded):
   a. Retrieve top-K relevant memories for this user
   b. Format as a system message: "What you know about this user: ..."
   c. Prepend system message before the session history
3. Inject history into request (existing)
```

Memories are only injected at the start of a new session — not on every turn — to avoid bloating context unnecessarily.

**Pipeline with memory:**

```
Request (new session)
  ↓
SessionMiddleware
  ├─ Load session history → empty (new session)
  ├─ Retrieve user memories → ["User's name is Arun", "Prefers concise answers", ...]
  ├─ Format system message → prepend to messages
  └─ Pass enriched request to cache → chat handler
```

**Persistence:** same delta-journal pattern as `SessionStore` — `memories.jsonl` per user, append on extract, compact on startup. No external service required.

**User key:** `tenantID:userID` from `identity.FromContext(ctx)`. Requires `X-IXR-UserID` header (already supported by the identity resolver). Sessions without a `UserID` don't accumulate memories.

**Extraction timing:** asynchronous, post-response, via the existing event bus. The `SessionMiddleware` publishes a `TurnEvent` after appending to the session store; a `MemoryPlugin` (implements `EventConsumer`) receives it, runs extraction, and writes to `UserMemoryStore`. Extraction never adds latency.

**Configuration:**

| Env var | Default | Meaning |
|---|---|---|
| `IXR_MEMORY=true` | *(unset)* | Enable user memory extraction and injection |
| `IXR_MEMORY_DIR` | *(unset)* | Journal directory; empty = memory-only |
| `IXR_MEMORY_TOP_K` | `5` | Max memories injected per new session |
| `IXR_MEMORY_EXTRACTOR` | `rule` | `rule` or `llm` |

---

### Gap 10 — Multimodal Input (Vision)

**What it is:** `schema.Message` gains the ability to carry image content alongside — or instead of — text, so a caller sending a vision request (screenshot + question, document image + extraction prompt) through ixr gets it forwarded correctly instead of silently losing the image.

**Why it matters:** `Message.Content` is a plain string today. Any caller sending OpenAI's or Anthropic's multi-part content shape (`"content": [{"type":"text",...},{"type":"image_url",...}]`) either fails JSON decoding or has the image silently dropped depending on how strictly the request is parsed. Vision-in-the-loop workflows (screenshot QA, document extraction, UI-testing agents) are now a mainstream usage pattern for exactly the kind of app ixr sits in front of — this is a hard blocker, not a nice-to-have, for that whole category of traffic.

**Design sketch:**

The core tension: OpenAI's wire format allows `"content"` to be *either* a bare string *or* an array of content-part objects — the same JSON key, two shapes. Changing `Message.Content`'s type outright would be a breaking change to every existing text-only integration. Following the same pattern used for tool-calling (Message gained `ToolCallID`/`Name` as additive fields, not a rewrite of `Content`), this adds a new field rather than repurposing the existing one:

```go
// pkg/schema/event.go
type Message struct {
    Role       string        `json:"role"`
    Content    string        `json:"content,omitempty"`
    Parts      []ContentPart `json:"-"` // populated by custom (Un)MarshalJSON when "content" arrives as an array
    ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
    ToolCallID string        `json:"tool_call_id,omitempty"`
    Name       string        `json:"name,omitempty"`
}

type ContentPart struct {
    Type     string        `json:"type"` // "text" | "image_url"
    Text     string        `json:"text,omitempty"`
    ImageURL *ImageURLPart `json:"image_url,omitempty"`
}

type ImageURLPart struct {
    URL    string `json:"url"`              // https:// or data: URI
    Detail string `json:"detail,omitempty"` // "auto" | "low" | "high"
}
```

`Message` needs a custom `UnmarshalJSON`/`MarshalJSON` pair to detect which shape `"content"` arrived as: a bare string decodes straight into `Content` (today's behaviour, unchanged); an array decodes into `Parts`, and `Content` is left empty unless the caller also wants a flattened-text fallback for adapters that don't support images (concatenate the `text` parts). Text-only callers see zero behavioural change — this is additive, matching the RFC's "unconfigured state equivalent to pre-feature state" constraint.

Per-adapter translation (same shape of work as the tool-calling adapter changes):
- **OpenAI / openaicompat** — wire format already matches `ContentPart` almost exactly; `wireMessage.Content` becomes polymorphic the same way, passed through close to as-is.
- **Anthropic** — image content blocks are `{"type":"image","source":{...}}`. Anthropic's source object supports *both* `{"type":"base64","media_type":"...","data":"..."}` and `{"type":"url","url":"..."}` — Anthropic fetches URL sources itself, so no outbound HTTP fetch needs adding to ixr's request path after all (the original concern about violating the latency-budget constraint doesn't apply). A `data:` URI decodes into the base64 form (extract media type + payload directly, no re-encoding); anything else passes through as a `url` source unchanged.
- **Google AI (Gemini)** — images are `inlineData` parts (`{"inlineData":{"mimeType":...,"data":"..."}}`), following the same `genPart`-as-one-of pattern already established for `functionCall`/`functionResponse`. Not yet implemented — v1 covers OpenAI, Anthropic, and openaicompat.

**Status: implemented.** OpenAI, openaicompat, and Anthropic (both `data:` URI and URL image sources). Live-verified against real Anthropic with a real generated image — correctly identified its color through the full JSON-polymorphism → adapter-translation → live-API path. Ollama is not a gap either: `internal/adapters/providers/ollama` is a thin wrapper over the shared openaicompat adapter (`openaicompat.New("ollama", ...)`), which already forwards `Parts` — this RFC previously listed Ollama alongside Google AI and Bedrock as open, which was stale/incorrect. Google AI and Bedrock genuinely remain open: their translation loops (`googleai/translate.go`, `bedrock/adapter.go`) only ever read `m.Content`, never `m.Parts`, so a caller's image is silently dropped and the request goes through as text-only rather than erroring — confirmed by test (`TestToGenWireRequest_ImageContentDroppedNotErroredOrCorrupted`, `TestBuildBody_ImageContentDroppedNotErroredOrCorrupted`). That's the documented graceful-degradation trade-off (the interface doesn't require every adapter to support it at once), but it was previously *silent* — a caller had zero signal their image was ignored. Both adapters now emit `slog.Warn` when a message carries `Parts` they can't translate, so it's at least visible to the operator instead of a mystery "why did the model ignore my image" report.

---

### Gap 11 — Model Chaining

**What it is:** Sequential multi-model chaining — a request names a chain instead of a single model, and ixr runs a fixed sequence of models, each step's output feeding into the next step's prompt, returning the final step's response to the caller.

**Why it matters:** This shipped as example config in `demo-ixr.yaml` (`fast-refine`, `smart-qa`, `debate` — draft-then-refine and multi-perspective synthesis patterns) before dogfooding found the feature itself was never implemented on `main`, so the block was removed to stop it silently no-opping. It's a genuinely useful pattern — self-refinement and ensemble debate are things teams build bespoke orchestration for today — and the closest thing OmniRoute names to it (`fusion`/`pipeline` routing strategies: parallel-panel-plus-judge, sequential pipeline) is presented as a first-class routing strategy, not a bolt-on. Reviving this properly, rather than leaving it as a documented-then-retracted feature, closes a real and specific parity gap.

**Design sketch:**

The previously-started implementation lived on `feature/model-chain` (`X-IXR-Chain` header), but that branch diverged before gaps 3/4/8/9 landed — its diff against current `main` touches 130 files with large deletions, which makes reviving it via rebase/cherry-pick riskier and slower than a fresh, small implementation against the current codebase. Reimplement using the config shape that already existed (and was already documented) rather than inventing a new one:

```yaml
# ixr.yaml
chains:
  fast-refine:
    models:  [llama-3.3-70b-versatile, mistral-small-latest]
    prompts: ["", "Improve the previous answer: fix any inaccuracies and make it more concise."]
```

```go
// internal/domain/chain/chain.go
type Chain struct {
    Name    string
    Models  []string
    Prompts []string // same length as Models; Prompts[0] is typically "" (use the caller's original messages verbatim); Prompts[i>0] wraps the running transcript with a refinement instruction for that step
}
type Registry map[string]Chain
```

Trigger: the chain name is passed as `"model"` — consistent with how every other routing decision in ixr keys off the model field, rather than introducing a second, parallel dispatch mechanism via a header. In `chat.go`, chain lookup happens before the normal `h.router(req.Model)` call: `if c, ok := h.chains[req.Model]; ok { h.handleChain(w, r, c, &req); return }`.

Execution: for step *i*, build a request whose messages are the caller's original messages, plus the prior step's assistant reply (for *i > 0*), plus a synthetic user turn carrying `Prompts[i]` (if non-empty), routed to `Models[i]` through the existing per-step `h.router` lookup and — since it already exists now — `routing.Execute` for retry-safety on each step. The response returned to the caller is the final step's response; intermediate steps are not exposed to the client in v1 (a debug/trace mechanism is a reasonable follow-up, not a blocker).

Config loading: `internal/adapters/config/schema.go` gains a `Chains map[string]ChainConfig` section; `pkg/ixr/ixr.go` builds the `chain.Registry` alongside the other registries at startup, same wiring shape as everything else there.

**Chain steps and Gap 5 (context-window escalation) — a known, narrower gap after the live-wiring fix below.** Each step routes via `internal/ingress/chain.go`'s `runChainStep`, which still builds a bare `routing.RoutingDecision{Model: model}` with no `FallbackChain`, so a context-length error mid-chain has nothing to escalate to and the step fails like any other error, aborting the chain. This note originally justified that as "not chain-specific — an explicit-model request outside chains has exactly the same limitation," which was true when it was written but is now stale: the live-wiring fix documented above gives every explicit catalog-model request (outside chains) a real `FallbackChain` via `routing.FallbackChainFor`. Chain steps were not updated to match, so they're now a genuinely narrower case than direct requests, not an equivalent one. `TestChatHandler_ChainStepContextOverflowAbortsCleanly` still holds — the failure is clean (502, well-formed JSON body, no hang or panic) — but "fix" now means giving `runChainStep` the same `FallbackChainFor` treatment `chat.go` has, which is a small, well-scoped follow-up rather than the "design per-step fallback chains from scratch" this note originally described.

---

### Gap 12 — Bandit Exploration in Primary Auto-Routing

**What it is:** `model:"auto"` currently resolves through a purely deterministic scorer (`scoring.Engine.Decide`: policy filter → live `ModelPerfStore` stats → static weighted score → pick highest). The epsilon-greedy/UCB bandit that already exists (`internal/domain/scoring/bandit.go`) only feeds the opt-in shadow-routing path (`X-IXR-Shadow-Model` header) — it has never influenced a real, primary routing decision.

**Why it matters:** This is the largest remaining gap between ixr's routing sophistication and what OmniRoute advertises (18 named routing strategies, including a bandit-driven "auto-combo engine" with progressive cooldown). ixr's deterministic scorer is reasonable but static: it can't discover that a given model is currently faster, cheaper, or more reliable than its configured priors suggest without an operator manually retuning `ModelCard` values. Wiring the existing bandit into primary decisions is what turns ixr's routing from well-tuned defaults into something that actually improves from live traffic — the specific claim OmniRoute makes that ixr currently doesn't back up anywhere outside the opt-in shadow path.

**Design sketch:**

Minimal-risk integration, given `scoring.Engine.Decide` is relied on by existing deterministic tests (`TestChatHandler_ModelAutoResolvesCatalog` asserts an exact model choice) that must keep passing when the feature is off:

- `Engine.SetBandit` (nil by default) lets `Decide` defer the primary pick to `Bandit.Select(candidates)` instead of always taking the top deterministic score. `EpsilonGreedy.Select` already implements the epsilon-vs-exploit split internally (untried arms tie, so it degrades to the deterministic pick until reward data exists) — `Decide` doesn't need its own probability check, just a call-or-don't-call decision.
- Reward recording reuses the existing formula: `internal/domain/scoring/shadow.go`'s orchestrator already computes `success*0.7 + (latency<2s)*0.3` and calls into the bandit for shadow routing; a new `plugins/banditreward` plugin applies the identical formula to real primary-routed `CallEvent`s. A new marker on `CallEvent` (`AutoRouted bool`, set in `chat.go`'s `model:"auto"` branch, both streaming and non-streaming) tells the plugin which events came from `model:"auto"` versus an explicit model name — explicit requests and shadow events are both ignored, since neither opted into the bandit's exploration.

**Status: implemented.** Opt-in via `IXR_AUTO_BANDIT=true`, default off — matching the RFC's core constraint that the unconfigured state must be equivalent to the pre-feature state; the existing deterministic auto-routing tests pass unchanged. Shares the same `EpsilonGreedy` instance already constructed for shadow routing, so exploration and reward from both paths reinforce one set of arm statistics. Unit-tested with a stub bandit forced to a specific pick, proving `Decide` actually defers control rather than silently ignoring it; live-verified that `AutoRouted` flows correctly into the audit trail and the plugin pipeline doesn't error under load.

---

### Gap 13 — No System-Level Admission Control

**What it is:** ixr has per-model circuit breaking (`internal/domain/circuitbreaker`) but nothing that answers "the system as a whole is out of capacity, shed load" independent of any single model's health. There is no cap on total in-flight requests and no fast-reject path that fires purely on aggregate load.

**Why it matters:** Found via a load-test profiling pass (`internal/ingress/loadprofile_test.go`, `IXR_LOADTEST=1`), not from a design review. At 100–200 concurrent workers on a 12-core machine — realistic load for the hardware — ixr was clean: 0 errors across ~477k requests, a +6 goroutine delta, no heap growth. At 500 workers (~42x the core count, admittedly an extreme, same-process test artifact where the load generator and server compete for the same cores) the picture changed: ~38% error rate, thousands of extra goroutines, tens of MB of extra heap.

Investigating *why* surfaced two distinct findings, not one:

1. **A real bug, now fixed:** every `circuitbreaker.RecordOutcome` call site treated a caller's `context.Canceled`/`context.DeadlineExceeded` identically to a genuine provider error. A client giving up says nothing about whether the model is healthy, but was being recorded as if it did — confirmed live: models never configured to fail (`gpt-4o`, `claude-haiku-4-5`) started tripping their breakers via context-canceled errors alone once enough callers were timing out simultaneously. Fixed with `shouldRecordOutcome` (`internal/ingress/chat.go`), gating all four `RecordOutcome` call sites (chat + chain, streaming + non-streaming). Regression-tested (`TestChatHandler_ClientCancellationDoesNotTripCircuitBreaker`): confirmed to fail without the fix and pass with it, plus a companion assertion that a genuine provider error still trips the breaker as before.

2. **This gap, not fixed:** re-running the same 500-worker scenario *after* the circuit-breaker fix did not improve the error rate or goroutine count — if anything, both were slightly worse (3978 vs 2432 extra goroutines). Reason: the old, buggy over-tripping was accidentally acting as crude load-shedding — a falsely-tripped breaker returns an instant 503 with zero work done, whereas a correctly-scoped one lets requests through to actually attempt the (already-congested) call. Fixing the correctness bug removed that accidental side effect without replacing it with anything, because nothing in ixr today plays that role on purpose.

**Design sketch (not implemented):** A bounded semaphore (buffered channel or `golang.org/x/sync/semaphore`) sized to a configurable max in-flight request count, acquired at the top of `ChatHandler.ServeHTTP` (and the chain equivalent) before any routing/provider work begins, with a non-blocking acquire — fail fast with 503 `system_overloaded` rather than queueing, matching the RFC's existing latency-budget non-negotiable. Distinct from circuit breaking by design: this rejects based on *aggregate* concurrency regardless of which model is requested, where circuit breaking rejects based on a *specific model's* observed failure rate regardless of system load. The two are complementary, not redundant — a fix to one is not a fix to the other, as this profiling pass demonstrated directly.

**Progressive cooldown — implemented.** The remaining piece of OmniRoute's "auto-combo engine with progressive cooldown" claim: an arm that fails `cooldownFailureStreak` (3) times in a row is excluded from `Select` for a backoff window that doubles with each additional consecutive failure (capped at 5 minutes), on both `EpsilonGreedy` and `UCB`. Any reward classified as a success clears the cooldown immediately rather than decaying gradually — recovery is fast, only the backoff on repeated failure is progressive. Failure/success is classified against the only reward source actually wired to `Update` today (`success*0.7 + fast*0.3`, so every success scores ≥ 0.7); this coupling is called out in-code (`internal/domain/scoring/bandit.go`) since the general weighted `Reward()` function isn't wired to a caller yet and would need the threshold revisited if it is. If every candidate is cooling down simultaneously, `Select` falls back to the full candidate list rather than starving routing entirely — cooldown is a preference, not a hard exclusion that can wedge the request path. Unit-tested including an adversarial always-failing arm and the all-arms-cooling-down degenerate case.

**Fusion / parallel-panel-plus-judge — implemented.** OmniRoute's other named routing strategy this RFC didn't yet cover: `chain.Chain` gained a `Strategy` field (`internal/domain/chain/chain.go`), `"sequential"` (the existing Gap 11 behavior, now the default) or `"fusion"` — Models run in parallel against the caller's original messages, then their combined answers route to `Judge` for synthesis (`internal/ingress/chain.go`'s `handleFusionChain`). A single panel member failing doesn't abort the request, only an all-panel failure does — the resilience a panel is supposed to buy you, and a deliberate difference from sequential's abort-on-any-failure semantics. Configured via `ixr.yaml`'s existing `chains:` block (`strategy: fusion`, `judge: <model>`); see `panel-judge` in `demo-ixr.yaml`. Live-verified end-to-end against real Anthropic/Groq/Mistral traffic, both streaming and non-streaming.

**Chain streaming bug fix.** Found while stress-testing this gap: `chat.go` dispatched `chains:` requests to `handleChain` before ever checking `req.Stream`, so a `stream:true` request against any chain silently got a plain JSON body back instead of SSE — confirmed live before the fix. The terminal call in both strategies (last sequential step, or the fusion judge) now honours `req.Stream`; earlier/panel calls stay non-streaming since they need each other's full text to build the next prompt or the judge's input.

**Routing-quality eval harness — implemented.** Everything above (bandit exploration, cooldown, fusion, and any future caller-feedback quality signal) is a mechanism for improving routing — none of it, on its own, proves routing is *actually* delivering good-cheap answers over time rather than just optimizing the proxy signals available at request time (`FinishReason`, cost estimates). `cmd/ixr-eval` (`internal/eval`) closes that gap: a golden question set (`eval/golden.yaml`) scored across any set of models — including `"auto"`, so auto-routing itself can be measured, not just its component models — via real HTTP calls to a running ixr instance (the actual request path: routing, plugins, cost accounting all included, not a shortcut through internal Go APIs). Scoring is deliberately simple (case-insensitive substring `must_contain`/`must_not_contain` checks, not an LLM judge) — cheap and deterministic enough to run repeatedly and track over time, consistent with the RFC's "no data leaves the process without explicit operator configuration" constraint applying here too. Reports per-model pass rate, average latency, and average/total cost (`make eval MODELS=gpt-4o,claude-sonnet-4-6,auto`). Unit-tested (`internal/eval/eval_test.go`): YAML parsing, substring scoring in both directions, a `ChatFunc`-error mid-run producing a failed result rather than aborting the rest of the eval, and summary aggregation (pass rate counts errors as failures, average cost excludes them since there's nothing priced to average in).

---

## Plugin Architecture as the Extension Model

The gaps above split into two categories by when they need to run:

**Post-call, async (EventConsumer):** budget accumulation (Gap 3), OTEL span emission (Gap 7), token-usage tracking, audit logging. These subscribe to `CallEvent` from the bus. The bus is non-blocking — a slow plugin never affects request latency.

**Pre-call, sync (RequestInterceptor):** PII guardrails (Gap 1), budget gate (Gap 3), hierarchical budget gate (Gap 8). These run in the request path and must be fast. They return an error to block, nil to pass through.

The `RequestInterceptor` interface (Gap 1) is the only new abstraction needed to address five of the seven remaining gaps. Everything else is either an existing plugin type or a change internal to a provider adapter.

The ingress pipeline with all interfaces wired looks like:

```
POST /v1/chat/completions
        ↓
   auth + rate limit middleware
        ↓
   [RequestInterceptor chain]  ← PII guardrail, budget gate  (sync, pre-call)
        ↓
   SessionMiddleware            ← inject user memories (new session) + history (all turns)
        ↓
   cache lookup
        ↓
   scoring engine → provider call
        ↓
   response to caller
        ↓
   SessionMiddleware            ← append new turn to session store
        ↓
   cache store (post-response)
        ↓
   [EventConsumer bus]  ← memory extraction, budget accumulate, OTEL span, audit-log  (async, post-call)
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

3. ~~**`IXR_CACHE_THRESHOLD` env var.**~~ **Resolved** — `IXR_CACHE_THRESHOLD` is implemented on `semantic-cache` branch.

4. ~~**Journal compaction.**~~ **Resolved** — startup compaction (temp+rename atomic rewrite) is implemented in both `PersistentSemanticBackend` and `PersistentSessionStore`.

5. **Budget persistence format.** Gap 3 (budget enforcement) uses a similar file-journal pattern to the semantic cache. Should the two share a persistence abstraction, or remain independent implementations? Sharing reduces code but couples unrelated features.

6. **Cross-instance cache.** A shared `SemanticBackend` (pgvector) behind the existing interface enables cache sharing across scaled deployments. The `SemanticBackend` interface already supports this — it is purely an implementation choice. When to add it depends on whether ixr targets single-instance or multi-instance deployments as the primary case.

7. **Memory extraction quality vs. latency tradeoff.** The `RuleExtractor` is fast and free but misses implicit facts. The `LLMExtractor` catches more but costs a small LLM call per turn. Should the extractor be configurable per tenant, or a single global setting? A hybrid (rule-first, LLM only when rules find nothing) may be the right default.

8. **Memory staleness and correction.** Users change — someone who said "I work at Acme" last year may work somewhere else now. There is no mechanism to update or invalidate an existing memory entry when a newer fact contradicts it. The `LLMExtractor` could detect contradictions and overwrite; the `RuleExtractor` cannot. This needs a resolution before memory is considered reliable.

9. **Streaming session capture.** Streaming responses currently receive history injection but the response is not captured back into the session store (v1 constraint). A v2 SSE assembler that reconstructs the assistant turn from chunks would close this gap without buffering the stream for the client.

10. ~~**Semantic cache false hits when session history is large.**~~ **Resolved** — option 2 below is implemented. Note this also required first merging the `semantic-cache` branch (Gap 2) into `main`/the integration branch: despite the RFC's status table listing it as implemented, that branch had never actually been merged anywhere reachable from the product's mainline (`git branch --all --contains semantic-cache` showed only the isolated branch and stale test branches) — it was implemented in isolation, then this exact false-hit bug was found in that isolated `test/all-features` integration testing and the branch sat unmerged.

    Originally: when `SessionMiddleware` prepends a rich conversation history to the request before the cache sees it, two semantically different questions in the same session can produce message lists that are similar enough to trigger a `SEMANTIC-HIT` against a prior cached entry. The injected history dominates the token overlap score, drowning out the difference in the new user turn.

    Two mitigations were evaluated:
    - **Raise `IXR_CACHE_THRESHOLD`** — 0.95+ reduces false hits at the cost of fewer legitimate semantic matches. Simple, operator-configurable, but a mitigation rather than a fix.
    - **Cache key excludes injected history — implemented.** `SessionMiddleware` now attaches `historyLen` to the request context via `cache.WithHistoryLen` (`internal/domain/cache/context.go`) right before calling `next`; `SemanticCache.Lookup`/`Store` read it via `cache.HistoryLenFromContext` and embed only `req.Messages[historyLen:]` — the caller's actual new turn — instead of the full history-plus-turn text. `ExactCache` is untouched: hashing the full message list (including history) is correct for exact-match, since an identical full conversation state genuinely warrants reuse; only the semantic (fuzzy) layer was drowning out the new turn. `historyLen` defaults to unset (0, full list) when no session middleware is in play, so direct/non-session cache usage sees no behavior change.

    Verified both at the cache layer (`internal/domain/cache/semantic_test.go`: a crafted false hit reproduces without `historyLen`, then the same requests correctly miss with it, and a genuine near-duplicate new turn still hits) and end-to-end with real `SessionMiddleware` → `CacheMiddleware` → `ChatHandler` wiring (`internal/ingress/session_cache_integration_test.go` — this composed chain had no test coverage at all before this fix). The end-to-end test was confirmed to actually fail without the fix (3 of 4 expected provider calls — the 4th, unrelated question false-hit) before being restored to green.

---

## Future Work

- **`OllamaEmbedder`** — `Embedder` backed by Ollama's `/api/embeddings` for paraphrase-level matching on the Store path.
- **HNSW in-memory index** — replace O(n) scan with approximate nearest-neighbour for deployments with >10k cached entries.
- **`PgvectorBackend`** — `SemanticBackend` backed by pgvector for cross-instance cache sharing.
- **External bus adapters** — NATS, Kafka, Kinesis, Google Pub/Sub behind `pkg/bus.Bus`.
- **Secrets rotation without restart** — Vault, AWS Secrets Manager, GCP Secret Manager for provider credentials.
- **`model: "auto"` as default** — when no model is specified, the scoring engine picks. Currently requires explicit opt-in.
- **Quality score in reward function** — the bandit scoring engine has a placeholder `δ * quality_score` term; wiring it requires a lightweight output quality signal (e.g. response length, format adherence, downstream error rate).
- **User memory & cross-session context (Gap 9)** — `UserMemoryStore` + `MemoryExtractor` + injection via `SessionMiddleware`. `RuleExtractor` first, `LLMExtractor` as opt-in upgrade.
- **Streaming session capture** — SSE assembler to reconstruct assistant turn from chunks and append to session store without buffering for the client.
- **Memory management API** — `GET /v1/memory` and `DELETE /v1/memory/:id` endpoints so users can inspect and correct their stored memories.
- **CNCF Sandbox submission** — the long-term governance target once the project reaches production stability across multiple adopters.
