# ixr

A tiny, fast, embeddable inference proxy written in Go.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/YashVishwas/ixr.svg)](https://pkg.go.dev/github.com/YashVishwas/ixr)

---

## what it is

ixr sits in front of every LLM call your service makes. you import it. you start it. you point your existing openai/anthropic client at it. nothing else changes.

every call now flows through a layer that is **schema-aware, observable, and extensible** — so any intelligence (security, finops, governance, adaptive routing) can be built on top of it without touching the calling service ever again.

## quickstart — 60 seconds

**path 1: embed in a Go service**

```go
import ixr "github.com/YashVishwas/ixr/pkg/ixr"

func main() {
    go ixr.Start() // that's it.
}
```

point your existing client at `http://localhost:7000` — nothing else changes:

```python
client = OpenAI(base_url="http://localhost:7000")
```

**path 2: run as a binary**

```bash
# build once
go build -o ixr ./cmd/ixr

# run with a config file, or just env vars
./ixr --config ixr.yaml
OPENAI_API_KEY=sk-... ./ixr
```

**path 3: docker**

```bash
docker build -t ixr .
docker run -p 7000:7000 -e OPENAI_API_KEY=sk-... ixr
```

**config (minimal)**

```yaml
# ixr.yaml
server:
  port: 7000

providers:
  openai:
    api_key: ${OPENAI_API_KEY}
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
```

## architecture

```
your service
    │
    │  openai sdk (base_url=localhost:7000)
    ▼
┌─────────────────────────────────────────┐
│                  ixr                    │
│                                         │
│  ingress → app → provider → response   │
│                ↓                        │
│            event bus                   │
│           /    |    \                   │
│       plugin plugin plugin             │
└─────────────────────────────────────────┘
    │             │
    ▼             ▼
 openai       anthropic
```

see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full layered model.

## writing your first plugin

```go
package main

import (
    "context"
    "encoding/json"
    "log/slog"

    "github.com/YashVishwas/ixr/pkg/plugin"
    "github.com/YashVishwas/ixr/pkg/schema"
)

type CostLogger struct{}

func (c *CostLogger) Name() string { return "cost-logger" }

func (c *CostLogger) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
    b, _ := json.Marshal(ev.Cost)
    slog.Info("call cost", "model", ev.Model, "cost", string(b))
    return nil
}

// register: pass to ixr.Start(ixr.WithPlugins(&CostLogger{}))
```

under 30 lines. no forks. see [docs/PLUGINS.md](docs/PLUGINS.md) for more.

## non-negotiables

1. **one line of code to insert** — anything more = failure
2. **one binary to run** — no container required, no helm, no sidecar mesh
3. **zero refactor in the calling service** — existing openai/anthropic sdks just work
4. **schema-first** — every call is a typed struct, exported on a bus
5. **extensible without forks** — plugins are go interfaces, loaded at startup
6. **routing that learns** — after enough traffic, routing gets smarter automatically
7. **opensource by default** — Apache 2.0, signed releases, public roadmap

## status

| phase | status | goal |
|-------|--------|------|
| phase 1 | 🚧 in progress | docker pull → working llm call → observed event loop |
| phase 2 | planned | production hardening + adaptive routing intelligence |

## metrics

From the latest hardening pass (`hardening/gap-and-stress-tests`) — real numbers, not aspirational:

- **310 tests passing** across 25 packages, 0 failures — `go build`, `go vet`, and `go test -race ./...` all clean
- **59 new test functions** added in this pass, including production-shaped concurrency tests (many goroutines, not the sequential single-caller pattern most of the suite otherwise uses)
- **4.2M+ fuzz executions, 0 crashes** — `FuzzMessage_ContentPolymorphism` against the multimodal JSON codec
- **Full `-race` suite runs in ~11s** from a cold test cache
- **3 real concurrency/correctness bugs found and fixed**, each with a test that fails without the fix and passes with it:
  - budget enforcement TOCTOU race — a burst of concurrent requests could blow through a hard spend ceiling before spend was ever recorded
  - semantic cache false-hits on session history — two unrelated questions in the same session could serve each other's cached answers
  - chain requests silently ignoring `stream:true` — SSE clients got a plain JSON body back instead
- **Live-verified against real providers** (Anthropic, Groq, Mistral) — fusion routing (parallel panel + judge) and chain streaming confirmed working end-to-end, not just under mocks

## license

Apache 2.0 — see [LICENSE](LICENSE).
