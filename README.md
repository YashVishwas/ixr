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

## license

Apache 2.0 — see [LICENSE](LICENSE).
