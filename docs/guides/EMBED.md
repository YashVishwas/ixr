# ixr — Go embed guide

Import ixr directly into your Go service. One function call starts the proxy inside your process — no separate binary, no sidecar, no container.

---

## Prerequisites

- Go 1.22 or later
- An API key for at least one provider (see [QUICKSTART.md](../QUICKSTART.md))

---

## 1. Add ixr to your module

```bash
go get github.com/YashVishwas/ixr
```

---

## 2. Start ixr with one line

```go
package main

import (
    "log/slog"
    "os"

    ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

func main() {
    if err := ixr.Start(); err != nil {
        slog.Error("ixr exited", "err", err)
        os.Exit(1)
    }
}
```

`ixr.Start()` blocks until `SIGINT` or `SIGTERM`. In a real service, run it in a goroutine alongside your own HTTP server:

```go
func main() {
    // ixr runs alongside your service on port 7000
    go func() {
        if err := ixr.Start(); err != nil {
            slog.Error("ixr exited", "err", err)
        }
    }()

    // your existing server continues here
    http.ListenAndServe(":8080", yourHandler)
}
```

---

## 3. Configure via options

**With a YAML config file:**

```go
ixr.Start(ixr.WithConfigFile("ixr.yaml"))
```

**Override the port:**

```go
ixr.Start(
    ixr.WithConfigFile("ixr.yaml"),
    ixr.WithPort(7001),
)
```

**Config file only, auto-discovered** — if no path is given, ixr looks for `ixr.yaml` in the working directory and up the tree:

```go
ixr.Start()   // discovers ixr.yaml automatically
```

**Environment variables only** — no config file needed. Set keys before starting:

```go
// ANTHROPIC_API_KEY must be set in the environment
ixr.Start()
```

---

## 4. Minimal config file

Create `ixr.yaml` alongside your binary. Add only the providers you have keys for:

```yaml
server:
  port: 7000

log_level: warn   # info | warn | error

providers:
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
  openai:
    api_key: ${OPENAI_API_KEY}
  llama:            # Groq-hosted Llama (free tier)
    api_key: ${GROQ_API_KEY}
  cerebras:         # free tier
    api_key: ${CEREBRAS_API_KEY}
  mistral:          # free tier
    api_key: ${MISTRAL_API_KEY}
  sambanova:        # free tier
    api_key: ${SAMBANOVA_API_KEY}
  github:           # GitHub Models (free tier)
    api_key: ${GITHUB_TOKEN}
  gemini:
    api_key: ${GOOGLE_API_KEY}
  gemma:
    api_key: ${GOOGLE_API_KEY}
  deepseek:
    api_key: ${DEEPSEEK_API_KEY}
  openrouter:
    api_key: ${OPENROUTER_API_KEY}
  zhipu:
    api_key: ${ZHIPU_API_KEY}
```

---

## 5. Full example service

```go
package main

import (
    "fmt"
    "log/slog"
    "net/http"
    "os"

    ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

func main() {
    // Start ixr on port 7000 (reads ANTHROPIC_API_KEY etc from env)
    go func() {
        if err := ixr.Start(ixr.WithConfigFile("ixr.yaml")); err != nil {
            slog.Error("ixr exited", "err", err)
            os.Exit(1)
        }
    }()

    // Your application server on port 8080
    http.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
        // Call ixr just like you'd call OpenAI — base URL is the only change
        fmt.Fprintln(w, "LLM calls go through ixr at http://localhost:7000")
    })

    slog.Info("app listening", "port", 8080)
    http.ListenAndServe(":8080", nil)
}
```

Run it:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
go run .
```

---

## 6. Point your existing LLM client at ixr

No changes needed beyond the `base_url`. Works with any OpenAI-compatible SDK.

**Python (in a separate process or script):**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:7000/v1",
    api_key="not-checked",   # ixr manages provider keys
)

response = client.chat.completions.create(
    model="claude-sonnet-4-6",      # explicit model
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)

# Or let ixr choose:
response = client.chat.completions.create(
    model="auto",
    extra_headers={
        "X-IXR-Task": "coding",
        "X-IXR-Budget": "2.0",
    },
    messages=[{"role": "user", "content": "Write a merge sort."}],
)
print(response.choices[0].message.content)
```

**From within Go (using the net/http client):**

```go
import (
    "bytes"
    "encoding/json"
    "net/http"
)

type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ChatRequest struct {
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
}

func askIXR(question string) (string, error) {
    body, _ := json.Marshal(ChatRequest{
        Model:    "claude-sonnet-4-6",
        Messages: []Message{{Role: "user", Content: question}},
    })

    resp, err := http.Post(
        "http://localhost:7000/v1/chat/completions",
        "application/json",
        bytes.NewReader(body),
    )
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    var result struct {
        Choices []struct {
            Message struct{ Content string } `json:"message"`
        } `json:"choices"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    return result.Choices[0].Message.Content, nil
}
```

---

## 7. Run the interactive demo

With ixr embedded and running, run the demo script against it from another terminal:

```bash
# Get the demo (demo_test branch)
git clone --branch demo_test --depth 1 \
  https://github.com/YashVishwas/ixr.git ixr-demo

# Run demo against your embedded instance
python3 ixr-demo/demo/run_demo.py --port 7000 --branch phase-2_2
```

The demo shows live routing decisions, multi-provider comparisons, shadow routing, and drops into interactive chat — all going through the ixr instance inside your process.

---

## Available options

| Option | Description |
|--------|-------------|
| `ixr.WithPort(n)` | Listen port (default: `7000`) |
| `ixr.WithConfigFile(path)` | Load config from a specific path |

ixr auto-discovers `ixr.yaml` if no `WithConfigFile` is provided.

---

## How provider credentials are resolved

Priority (highest first):

1. Environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.)
2. Values in `ixr.yaml` (which can themselves reference env vars via `${VAR}`)

If the same provider appears in both, the environment variable wins.

---

## Troubleshooting

**"ixr: no providers configured"**
No API keys were found at startup. Make sure at least one is exported before calling `ixr.Start()`.

**Port conflict with your own server**
```go
ixr.Start(ixr.WithPort(7001))
```

**ixr exits before my app is ready**
Start ixr in a goroutine and give it a moment before your first LLM call:

```go
go ixr.Start(...)
time.Sleep(500 * time.Millisecond)   // or use a readiness check
```
