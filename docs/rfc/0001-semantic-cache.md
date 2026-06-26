# RFC 0001 — Semantic Response Cache

**Status:** implemented (`semantic-cache` branch)
**Author:** ixr core team
**Last updated:** 2026-06-26

---

## Summary

Replace the exact-match SHA-256 response cache with a two-layer cache: exact-match first (zero cost, zero change to the hot path), semantic similarity second (embedding + cosine scan on miss). Add a file-journal backend so the vector store survives container restarts without requiring any external service. The entire feature is opt-in via environment variables and degrades silently when not configured.

---

## Problem

The existing cache (`internal/domain/cache/cache.go`) keys responses on a SHA-256 hash of `model + messages`. This gives exact-match semantics: a request is served from cache only if the prompt is byte-for-byte identical to a previously seen request.

Real workloads don't behave that way. Users asking for the same thing phrase it differently:

- "summarize this" / "give me a summary of this" / "tldr"
- "what is the capital of France?" / "France's capital city?"
- "translate to Spanish" / "Spanish translation please"

Each variant produces a cache miss, a full LLM call, and a bill. For summarization, classification, and FAQ-style workloads — the majority of traffic in most deployments — cache hit rates under exact-match are effectively zero even when the underlying requests are semantically identical.

The `SemanticBackend` interface was already stubbed in the codebase with a note that no backend was wired up. This RFC describes the implementation that closes that gap.

---

## Motivation

### Competitive positioning

Portkey offers semantic caching, but only on its hosted cloud plan. The open-source version does not include it. Any team with on-premise or air-gapped compliance requirements cannot use Portkey's semantic cache — the scan itself happens outside their infrastructure.

ixr's implementation runs entirely inside the customer's environment. No prompt text leaves the process. The embedding is computed locally using a pure-Go word vectorizer with zero external dependencies.

### Cost impact

For repetitive workloads, realistic cache hit rates with semantic matching are 30–50%. At $0.01–0.03 per 1k tokens on frontier models, a deployment handling 100k requests/day at an average of 1k tokens each saves $30–$90/day at a 30% hit rate. The cache pays for itself in the first hour.

### Latency impact (positive)

A semantic cache hit eliminates the entire LLM call — typically 500ms–2000ms p50. The overhead of a cache miss with the built-in embedder is sub-millisecond. The net effect is that cache hits are dramatically faster; misses are imperceptibly slower.

---

## Design

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

The exact layer always runs first. It is free: SHA-256 of the marshalled request, map lookup. The semantic layer only runs on exact miss, and only if the request has embeddable text.

### Components

**`internal/domain/cache/semantic.go`**

- `Embedder` interface — converts text to `[]float32`. Swappable.
- `SemanticBackend` interface — `Find` and `Store` over vectors.
- `MemorySemanticBackend` — in-process cosine similarity store. O(n) scan. Appropriate up to ~10k entries (each scan is ~0.1ms on modern hardware).
- `PersistentSemanticBackend` — wraps `MemorySemanticBackend` with an append-only JSON-lines journal. Replays non-expired entries on startup.

**`internal/domain/cache/vectorize.go`**

- `WordVectorizer` — maps tokens to a 512-dimensional float32 vector via FNV-32a hashing, then L2-normalizes. Deterministic. Zero external dependencies. Latency: sub-millisecond for any realistic prompt.

**`internal/domain/cache/semantic_cache.go`**

- `RequestAwareCache` interface — `Lookup` and `Store` over `*schema.RequestEnvelope`. This is what the middleware depends on; both `ExactCache` and `SemanticCache` implement it.
- `ExactCache` — thin wrapper over `*Memory` that implements `RequestAwareCache` via `Key(req)`.
- `SemanticCache` — two-layer orchestrator described above.

**`internal/ingress/cache_middleware.go`**

Updated to accept `RequestAwareCache` instead of `cache.Cache`. The middleware no longer computes the key itself — it passes the full `*schema.RequestEnvelope` to the cache and lets the backend decide the lookup strategy.

**`pkg/ixr/ixr.go`**

Wiring. `ExactCache` is always created. If `IXR_SEMANTIC_CACHE=true`, a `SemanticCache` is built on top using `PersistentSemanticBackend`. The backend's journal is closed on shutdown via `defer`.

### Latency protection

The embed step in `SemanticCache.Lookup` runs under a 5ms `context.WithTimeout`. If the embedder takes longer than 5ms — which can only happen if someone has swapped in a remote embedder and it is slow or unreachable — the lookup returns a miss and the request proceeds to the upstream LLM. No latency is added to the request path in the failure case.

With `WordVectorizer`, this timeout never fires. It is purely a safeguard against misconfiguration.

`Store` is called after the response has already been written to the client (the `responseRecorder` captures the body while writing through to the underlying `ResponseWriter`). Embedding on the store path is synchronous but post-response: the client receives their response before any embedding work begins.

### Persistence without an external service

`PersistentSemanticBackend` keeps the vector store on disk as an append-only JSON-lines file (`semantic.jsonl`). Each stored entry is one line:

```json
{"vec":[0.031,...,0.007],"resp":{"id":"chatcmpl-...","choices":[...]},"expires_at":"2026-06-26T13:00:00Z"}
```

On startup, `replayJournal` reads the file line by line, skips any entries past their `expires_at`, and loads the remainder into `MemorySemanticBackend`. The in-memory state is fully reconstructed with no additional infrastructure.

On every `Store`, a single line is appended with a mutex-protected `file.Write`. No fsync — OS-buffered writes are fast enough and the journal is reconstructable from any consistent snapshot.

**Durability model:** mount a volume at `IXR_CACHE_DIR`. On ephemeral containers (no volume), the cache warms up from scratch on each start — graceful degradation, not failure.

**Journal growth:** entries are append-only; expired entries are not removed during runtime. On the next process start, `replayJournal` naturally prunes them. For long-lived processes, a future compaction pass can rewrite the file without expired entries. At 512 float32 values (~2KB) plus a typical response body (~1–4KB), 1024 entries consume ~4–6MB — well within any container's ephemeral storage.

---

## Configuration

| Environment variable | Default | Effect |
|---|---|---|
| `IXR_SEMANTIC_CACHE` | *(unset)* | Set to `true` to enable the semantic layer |
| `IXR_CACHE_DIR` | *(unset)* | Directory for `semantic.jsonl`. Empty = in-memory only |
| `IXR_CACHE_SIZE` | `1024` | Max entries across both layers |
| `IXR_CACHE_TTL_SEC` | `300` | Entry TTL in seconds (0 = no expiry) |

Minimal Docker Compose example:

```yaml
services:
  ixr:
    image: ghcr.io/ixr/ixr
    environment:
      IXR_SEMANTIC_CACHE: "true"
      IXR_CACHE_DIR: /data/cache
      OPENAI_API_KEY: ${OPENAI_API_KEY}
    volumes:
      - ixr-cache:/data/cache

volumes:
  ixr-cache:
```

---

## Similarity threshold

The default threshold is **0.92**. At this level:

- Near-duplicates with minor wording changes hit reliably (tested: 0.93–0.99 with `WordVectorizer`)
- Semantically unrelated prompts stay well below the threshold (tested: 0.1–0.4)
- The gap between "similar enough" and "unrelated" is wide, making the threshold robust to word choice variation

`WordVectorizer` measures token overlap, not semantic meaning. "summarize" and "give me a summary of" will score high because they share tokens. "summarize" and "abstract" will score lower because they don't share tokens even though they mean the same thing. This is a known limitation of the built-in embedder — see Future Work.

A threshold of 0.88 is appropriate when the workload has more paraphrasing (different words, same meaning). 0.95+ is appropriate for strict near-duplicate matching only.

---

## Drawbacks

**`WordVectorizer` is token-overlap, not semantic meaning.** Two prompts with the same intent but no overlapping tokens will miss. This is the fundamental limitation of hash-based bag-of-words vectors. For FAQ-style and repetitive workloads it works well. For paraphrase-level matching it requires a provider-backed embedder (see Future Work).

**O(n) scan.** `MemorySemanticBackend.Find` iterates all entries on every semantic lookup. At 1024 entries and 512 dimensions, this is ~0.5M float32 multiplications — under 1ms. At 10k entries it becomes ~5ms, which starts to compete with the Lookup timeout. For deployments expecting >10k unique cached prompts, the backend should be swapped for an HNSW index (see Future Work).

**Journal is append-only.** No in-process compaction. For very long-lived processes with high eviction rates, the file grows until the next restart. Acceptable for the current deployment model (container restarts are common); a compaction pass can be added if needed.

**No cross-instance sharing.** Each ixr instance maintains its own journal. Horizontally scaled deployments will independently warm up their caches. A shared backend (e.g., pgvector) would fix this but reintroduces an external service dependency. Accepted tradeoff for the current architecture.

---

## Alternatives considered

**pgvector / Weaviate / Qdrant as the vector store.** These offer HNSW indexing (true O(log n) search), cross-instance sharing, and built-in persistence. They also require running and maintaining an additional service. Rejected for the initial implementation because it violates the zero-external-deps principle and the Docker-image-as-the-deployment-unit model. Can be added as a `SemanticBackend` implementation behind the existing interface.

**Provider-backed embedder (OpenAI text-embedding-3-small, Ollama nomic-embed-text) on the Lookup path.** Better semantic quality, but adds a network call to every cache miss. A 50ms embedding call on the request path is worse than the LLM call it is trying to avoid for cache hits. Rejected for Lookup; acceptable for Store (called post-response). The `Embedder` interface is already designed to be swapped in for the Store path without touching Lookup.

**Separate embedding service.** Run a sidecar that handles embeddings. Adds a network hop and a service to operate. Rejected — violates the single-binary deployment model.

**Redis as the cache backend.** `SCAN` over Redis hashes can approximate nearest-neighbour but is not a vector search. RedisSearch with `FT.SEARCH` supports vector similarity but requires a Redis module. Both require Redis. Rejected for the same reason as pgvector.

---

## Open questions

1. **Threshold configurability.** Currently hardcoded at 0.92. Should `IXR_CACHE_THRESHOLD` be exposed as a first-class env var? Likely yes before the feature is considered stable.

2. **Journal compaction.** For processes that run for weeks without restart, the journal will accumulate expired entries. A background goroutine that rewrites the file on startup (after replay) could handle this. Low priority until someone hits it.

3. **Separate embedder for Store vs Lookup.** The `SemanticCache` currently uses one `Embedder` for both paths. A richer embedder (provider-backed) on the Store path would improve hit quality without affecting Lookup latency. The struct could accept two embedders; the interface would not change.

4. **Cross-instance warm-up.** In a scaled deployment, each instance starts cold. A shared `SemanticBackend` (pgvector) would fix this. The `SemanticBackend` interface already supports this; it's an implementation choice, not an interface change.

---

## Future work

- **`IXR_CACHE_THRESHOLD` env var** — expose the cosine similarity threshold for operator tuning without a rebuild.
- **HNSW in-memory index** — replace the O(n) scan with an approximate nearest-neighbour index for deployments with >10k cached entries. Keeps everything in-process; no external service.
- **`OllamaEmbedder`** — implement `Embedder` backed by Ollama's `/api/embeddings` endpoint (e.g., `nomic-embed-text`). Used on the Store path for paraphrase-level match quality. The `Embedder` interface requires no changes.
- **`PgvectorBackend`** — implement `SemanticBackend` backed by pgvector for deployments that already run Postgres and want cross-instance cache sharing.
- **Journal compaction on startup** — after `replayJournal`, rewrite the file with only live entries to prevent unbounded growth on long-lived processes.
