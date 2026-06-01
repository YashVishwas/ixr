# ixr — Quick Start

ixr is an embeddable inference proxy. You point your existing OpenAI/Anthropic client at it and it handles provider routing, observability, and adaptive scoring without any changes to your code.

---

## Pick your method

| Method | Best for | Requires |
|--------|----------|----------|
| [Binary](guides/BINARY.md) | Trying it out, sidecar deployment | Nothing — just download |
| [Docker](guides/DOCKER.md) | Containerised services, CI | Docker |
| [Go embed](guides/EMBED.md) | Embedding directly in your Go service | Go 1.22+ |

---

## Step 0 — Get at least one API key

ixr needs credentials for at least one LLM provider. Free-tier options that need no credit card:

| Provider | Env var | Sign up |
|----------|---------|---------|
| Cerebras | `CEREBRAS_API_KEY` | cerebras.ai |
| Groq (Llama) | `GROQ_API_KEY` | console.groq.com |
| Mistral | `MISTRAL_API_KEY` | console.mistral.ai |
| SambaNova | `SAMBANOVA_API_KEY` | cloud.sambanova.ai |
| GitHub Models | `GITHUB_TOKEN` | github.com/marketplace/models |

Paid-tier options (pay-per-token):

| Provider | Env var |
|----------|---------|
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| Google AI | `GOOGLE_API_KEY` |

Set whichever key(s) you have in your shell before running any of the guides below:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
# or
export GROQ_API_KEY="gsk_..."
# or both — ixr will use all that are configured
```

---

## Step 1 — Choose a guide

- **[Binary guide](guides/BINARY.md)** — download a pre-built binary, run it, try the demo
- **[Docker guide](guides/DOCKER.md)** — build or pull the image, run as a container
- **[Go embed guide](guides/EMBED.md)** — import ixr into your own Go service

---

## How ixr routes requests

Once running, ixr listens on `http://localhost:7000` (or your configured port) and accepts standard OpenAI-shaped requests:

```bash
curl http://localhost:7000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

You can also let ixr choose the model:

```bash
curl http://localhost:7000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-IXR-Task: coding" \
  -H "X-IXR-Budget: 2.0" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "Write a binary search in Python."}]
  }'
```

Key routing headers:

| Header | Values | Effect |
|--------|--------|--------|
| `X-IXR-Task` | `coding` `reasoning` `math` `multilingual` | Boosts capability weight in scorer |
| `X-IXR-Budget` | e.g. `0.5` (USD per 1M tokens) | Filters out models above the cost cap |
| `X-IXR-Latency` | `sensitive` | Boosts latency weight in scorer |
| `X-IXR-Shadow-Model` | any model name | Fires a background shadow call for comparison |
| `X-IXR-UseCase` | any string | Tags all bus events with a business label |

---

## Supported providers and model prefixes

| Prefix / pattern | Provider | Key |
|-----------------|----------|-----|
| `claude-*` | Anthropic | `ANTHROPIC_API_KEY` |
| `gpt-*`, `o1*`, `o3*` | OpenAI | `OPENAI_API_KEY` |
| `gemini*` | Google AI (Gemini) | `GOOGLE_API_KEY` |
| `gemma*` | Google AI (Gemma) | `GOOGLE_API_KEY` |
| `gpt-oss*`, `qwen3*`, `llama-4-maverick*` | Cerebras | `CEREBRAS_API_KEY` |
| `mistral-*`, `codestral*`, `magistral*`, `devstral*` | Mistral | `MISTRAL_API_KEY` |
| `meta-llama*`, `deepseek-v3.2`, `gemma-3-12b-it` | SambaNova | `SAMBANOVA_API_KEY` |
| `openai/*` (with slash) | GitHub Models | `GITHUB_TOKEN` |
| `*/` (other slash) | OpenRouter | `OPENROUTER_API_KEY` |
| `glm-*` | Zhipu (Z.ai) | `ZHIPU_API_KEY` |
| `llama*` (other) | Groq | `GROQ_API_KEY` |
| `deepseek*` (other) | DeepSeek | `DEEPSEEK_API_KEY` |
| `model: "auto"` | ixr scoring engine picks | any configured key |
