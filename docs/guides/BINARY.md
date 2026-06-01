# ixr — Binary guide

Run ixr as a standalone binary. No Go installation, no Docker — just download, configure, and run.

---

## Prerequisites

- An API key for at least one provider (see [QUICKSTART.md](../QUICKSTART.md))
- macOS, Linux, or Windows

---

## 1. Get the binary

**Option A — Download from GitHub Releases** (recommended for production)

Go to [github.com/YashVishwas/ixr/releases](https://github.com/YashVishwas/ixr/releases) and download the file matching your machine:

| Your machine | File to download |
|---|---|
| Mac — Apple Silicon (M1/M2/M3/M4) | `ixr-darwin-arm64` |
| Mac — Intel | `ixr-darwin-amd64` |
| Linux | `ixr-linux-amd64` |
| Windows | `ixr-windows-amd64.exe` |

Make it executable (Mac/Linux):

```bash
chmod +x ixr-darwin-arm64   # or ixr-darwin-amd64 / ixr-linux-amd64
```

**Option B — Build locally from source**

Requires Go 1.22+. Produces all platforms in one command:

```bash
git clone https://github.com/YashVishwas/ixr.git
cd ixr
make dist
# binaries land in dist/
```

---

## 2. Create a config file

Create `ixr.yaml` in the same directory as the binary. You only need entries for the keys you actually have — ixr skips empty ones.

```yaml
# ixr.yaml
server:
  port: 8080

log_level: info

providers:
  # Paid
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
  openai:
    api_key: ${OPENAI_API_KEY}

  # Free tier
  cerebras:
    api_key: ${CEREBRAS_API_KEY}
  llama:                          # Groq-hosted Llama
    api_key: ${GROQ_API_KEY}
  mistral:
    api_key: ${MISTRAL_API_KEY}
  sambanova:
    api_key: ${SAMBANOVA_API_KEY}
  github:
    api_key: ${GITHUB_TOKEN}

  # Others
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

The `${VAR}` syntax is expanded from your environment at startup. You can also hard-code values, but env vars are safer.

---

## 3. Set your API key(s) and run

**Mac / Linux:**

```bash
export ANTHROPIC_API_KEY="sk-ant-..."   # or whichever key you have
./ixr-darwin-arm64 --config ixr.yaml    # Mac Silicon
# ./ixr-darwin-amd64 --config ixr.yaml  # Mac Intel
# ./ixr-linux-amd64  --config ixr.yaml  # Linux
```

**Windows (PowerShell):**

```powershell
$env:ANTHROPIC_API_KEY = "sk-ant-..."
.\ixr-windows-amd64.exe --config ixr.yaml
```

You should see:

```
2026/06/01 12:00:00 INFO ixr listening port=8080
```

ixr is now running at `http://localhost:8080`.

---

## 4. Verify it works

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Say: ixr works!"}]
  }'
```

You should receive an OpenAI-shaped JSON response.

**Quick auto-routing test** — ixr picks the model for you:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-IXR-Task: coding" \
  -H "X-IXR-Budget: 2.0" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "What is a hash table?"}]
  }'
```

---

## 5. Point an existing client at ixr

No code changes needed — just change the base URL:

**Python (openai SDK):**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="not-checked",   # ixr uses its own provider keys
)

response = client.chat.completions.create(
    model="claude-sonnet-4-6",   # or "auto"
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

**Node.js (openai SDK):**

```javascript
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://localhost:8080/v1",
  apiKey:  "not-checked",
});

const resp = await client.chat.completions.create({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "Hello!" }],
});
console.log(resp.choices[0].message.content);
```

---

## 6. Run the interactive demo

The demo shows ixr's routing decisions, multi-provider comparison, and shadow testing, then drops into an interactive chat.

```bash
# Get the demo script (demo_test branch)
git clone --branch demo_test https://github.com/YashVishwas/ixr.git ixr-demo

# ixr must already be running (step 3 above)
# Tell the demo script which port ixr is on
python3 ixr-demo/demo/run_demo.py --port 8080 --branch phase-2_2
```

The demo detects which API keys you have set and adapts its scenarios automatically.

---

## CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | auto-discover | Path to `ixr.yaml` |
| `--port` | `8080` | Override the listen port |

Port in the `--port` flag takes precedence over the config file.

---

## Troubleshooting

**"ixr: no providers configured"**
No API keys were found. Make sure you `export` the key before running, or put it directly in `ixr.yaml`.

**"no provider found for model X"**
The model prefix doesn't match any configured provider. Check the [provider prefix table](../QUICKSTART.md#supported-providers-and-model-prefixes).

**Port already in use**
```bash
./ixr-darwin-arm64 --config ixr.yaml --port 8081
```

**Mac — "cannot be opened because the developer cannot be verified"**
```bash
xattr -dr com.apple.quarantine ./ixr-darwin-arm64
```
