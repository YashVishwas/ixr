# ixr context compression — status and roadmap

Where ixr stands on "find the best answer as cheaply as possible" via
input/output token reduction, benchmarked against two external tools that
prompted this work: [Headroom](https://github.com/RaghavRD/headroom)
(input-side, content-aware context compression) and
[Caveman](https://github.com/juliusbrussee/caveman) (output-side, prompt-based
verbosity steering). Neither tool's source was read or copied at any point —
comparisons below are against their public READMEs only. Everything in ixr is
an independent implementation, built into ixr's existing request pipeline
rather than as a bolted-on external tool.

## Comparison

| Capability | Source of the idea | ixr status | Gap |
|---|---|---|---|
| Reversible compression (store original, retrieve on demand) | Headroom (CCR) | **Done** — `internal/domain/retrieval` + `plugins/compressor.NewReversible` + `chat.go`'s `resolveRetrieval` | Simpler: one bounded hop, in-memory-only store (no persistence, no cross-instance sharing), no cross-agent scope |
| JSON-structure-aware compression | Headroom (SmartCrusher) | **Done, partial** — `plugins/compressor/json.go`: schema+rows dedup for arrays of matching-schema objects, compact re-marshal fallback otherwise | Structural only, no semantic understanding; SmartCrusher likely handles more shapes (partial-schema, nested) |
| Code-aware compression | Headroom (CodeCompressor, AST-based) | **Scoped, not built** — see "Next up" below | Heuristic comment-stripping only, no real AST; true per-language parsing is a much larger, separately-scoped effort |
| ML-model-based general text compression | Headroom (Kompress-v2-base) | **Not built** | ixr uses line heuristics (`collapseBlankLines`/`collapseRepeatedLines`) for non-JSON, non-code text — a different order of magnitude from a trained model |
| Image compression | Headroom | **Not built** | Not attempted |
| Provider prompt-cache alignment | Headroom (CacheAligner + live-zone compression) | **Done, provider-specific** — Anthropic `cache_control` on the system block (`internal/adapters/providers/anthropic/translate.go`) | Anthropic-only (OpenAI does this server-side automatically; other providers not investigated). Not actively coordinated with the compressor — confirmed *no conflict today* (compressor never touches the system block), but there's no live-zone mechanism keeping a growing history's cacheable prefix stable as compression runs turn over turn |
| Output-token reduction (verbosity steering) | Caveman | **Done, simpler mechanism** — `plugins/brevity`: a fixed system-prompt instruction | Caveman describes "verbosity steering and effort routing," which may include real generation-parameter tuning, not just a prompt nudge |
| One-time persistent-memory compression | Caveman (`/caveman-compress`) | **Evaluated, not needed** — ixr's memory entries (`internal/domain/memory`) are already short, deduped-by-category, capped-at-topK facts; there's no bloat to compress | — |
| Cross-agent dedup memory | Headroom | **Not built, likely doesn't fit** | ixr is a single-gateway proxy, not a multi-agent memory layer — this concept may not map onto ixr's architecture at all |
| Failure mining / learning loop | Headroom (`headroom learn`) | **Not built** | Different category of feature (offline session analysis), not really compression |

## Checklist

### Done (each shipped as its own branch/PR, independently mergeable)

- [x] Request coalescing (`singleflight` on cache miss) — `feat/cache-request-coalescing`
- [x] Semantic cache quality tier (real embedder, bounded fallback) — `feat/semantic-cache-real-embedder`
- [x] Bandit reward quality signal (`FinishReason`-derived) — `feat/bandit-reward-quality-signal`
- [x] Anthropic prompt caching (`cache_control`) — `feat/anthropic-prompt-caching`
- [x] Request compressor extension point (reuses `guardrail.RequestInterceptor`) — `feat/request-compression-transformer`
- [x] Output-side brevity steering + Anthropic multi-system-message fix — `feat/brevity-output-steering`
- [x] Compressor/`cache_control` coordination regression tests — `feat/cache-compressor-coordination-tests`
- [x] JSON-structure-aware compression — `feat/json-aware-compression`
- [x] Reversible compression (retrieval store + synthetic tool + one-hop resolution) — `feat/reversible-compression`
- [x] `feat/routing-observability` — merged to `main`

### Not yet done

- [ ] **Code-comment stripping** (scoped above — heuristic, not AST; next up)
- [ ] True AST-aware code compression (large, separate effort — needs a parser per language; deliberately deferred, see "Do we need this?" discussion below)
- [ ] ML-model-based general-text compression
- [ ] Image compression
- [ ] Live-zone coordination between the compressor and `cache_control` (currently: confirmed non-conflicting, not actively cooperating)
- [ ] Provider-agnostic prompt-cache alignment (currently Anthropic-only)
- [ ] Cross-agent dedup memory (may not fit ixr's architecture — needs a scoping pass before deciding, not just a build)

## Open questions / needs investigation

Found while building the above, not yet acted on:

1. **Bedrock silently drops every system message.** `internal/adapters/providers/bedrock/adapter.go`'s `buildBody` (line ~117) does `if m.Role == "system" { continue }` with a comment claiming "system messages handled separately in full Anthropic Bedrock schema" — but there is no such handling anywhere in the file; the request struct has no `System` field at all. This is more severe than the Anthropic multi-system-message bug (already fixed on `feat/brevity-output-steering`): it's not "loses all but the last," it's "loses all of them, always," for every Claude-family model called via Bedrock. Not yet fixed.
2. **Only 4 of ixr's provider adapters were checked for system-message handling** (Anthropic — was buggy, fixed; Bedrock — buggy, not fixed; OpenAI/openaicompat — fine, native array support; GoogleAI — fine, already accumulates correctly). Cerebras, DeepSeek, Llama, llamacpp, Mistral, Ollama, OpenRouter, SambaNova, Zhipu, GitHub Models haven't been audited for the same class of bug.
3. **Anthropic cost accounting doesn't know about the cache discount yet.** `cost.ForUsage` prices all of `PromptTokens` at the full input rate, so ixr's own cost tracking currently overstates cost for any call with a nonzero `CacheReadInputTokens`. Flagged when `cache_control` was built; not fixed since the pricing catalog has no notion of a per-provider cache-read multiplier yet.
4. **Retrieval store is in-memory and single-instance only.** If ixr runs as more than one process/replica, a retrieval ID stored on the instance that handled the original request can't be resolved by a different instance handling the follow-up — the retrieve call would just report "no longer available" (graceful, but silently loses the reversibility benefit) rather than erroring. Not an issue for a single-instance deployment; worth a design pass before this ships in a multi-instance setup.
5. **"Do we need AST-aware code compression?"** — evaluated and deliberately deferred: Headroom's own numbers show JSON compression (60–95%) applies broadly across virtually any tool-calling/RAG workload, while code-specific gains (their highest numbers: 92% on code search/SRE debugging) are concentrated in coding-agent-shaped workloads specifically — a narrower slice of ixr's likely user base relative to the engineering cost (a parser per language). The heuristic comment-stripper above is the scoped middle ground.

## Licensing note

Every comparison in this doc and every piece of ixr code it describes came
from reading Headroom's and Caveman's public README documentation only —
never their source. Product concepts and stated goals aren't copyrightable;
only specific code expression is, and none of ixr's implementation shares
any with either project (different algorithms, different architecture, built
into ixr's own request pipeline rather than as a standalone tool).
