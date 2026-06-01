# ixr — Docker guide

Run ixr as a Docker container. Works on any machine with Docker installed, including Apple Silicon via Docker Desktop.

---

## Prerequisites

- Docker (Docker Desktop on Mac/Windows, Docker Engine on Linux)
- An API key for at least one provider (see [QUICKSTART.md](../QUICKSTART.md))

---

## 1. Get the image

**Option A — Build from source** (always up to date with your branch)

```bash
git clone https://github.com/YashVishwas/ixr.git
cd ixr
docker build -t ixr .
```

The Dockerfile uses a two-stage build: Go builder → `scratch` final image (~7 MB).

**Option B — Pull from GitHub Container Registry** (official tagged releases)

```bash
docker pull ghcr.io/yashvishwas/ixr:latest
# or a specific version
docker pull ghcr.io/yashvishwas/ixr:v0.1.0
```

The registry image is multi-arch (`linux/amd64` and `linux/arm64`), so Docker Desktop on Apple Silicon pulls the right one automatically.

---

## 2. Run with environment variables (simplest)

Pass your API keys directly on the command line. ixr reads them at startup.

```bash
docker run -d \
  --name ixr \
  -p 8080:8080 \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  ixr
```

Multiple keys:

```bash
docker run -d \
  --name ixr \
  -p 8080:8080 \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -e GROQ_API_KEY="gsk_..." \
  -e CEREBRAS_API_KEY="csk-..." \
  ixr
```

---

## 3. Run with a config file (recommended)

Create `ixr.yaml` locally first (use the template from the [Binary guide](BINARY.md#2-create-a-config-file)), then mount it into the container:

```bash
docker run -d \
  --name ixr \
  -p 8080:8080 \
  -v "$(pwd)/ixr.yaml:/ixr.yaml:ro" \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  ixr --config /ixr.yaml
```

The `${ANTHROPIC_API_KEY}` placeholders in `ixr.yaml` are resolved from the container's environment variables, so you still pass keys via `-e` rather than hard-coding them in the file.

---

## 4. Verify it works

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Say: ixr in Docker works!"}]
  }'
```

Check logs:

```bash
docker logs ixr
docker logs -f ixr   # follow
```

---

## 5. Docker Compose

For projects that already use Compose, add ixr as a service:

```yaml
# docker-compose.yml
services:
  ixr:
    image: ixr                    # or ghcr.io/yashvishwas/ixr:latest
    build: .                      # omit if using the registry image
    ports:
      - "8080:8080"
    environment:
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
      GROQ_API_KEY:      ${GROQ_API_KEY}
      CEREBRAS_API_KEY:  ${CEREBRAS_API_KEY}
    volumes:
      - ./ixr.yaml:/ixr.yaml:ro
    command: ["--config", "/ixr.yaml"]
    restart: unless-stopped

  your-app:
    build: ./your-app
    environment:
      LLM_BASE_URL: http://ixr:8080/v1   # point your app at ixr
    depends_on:
      - ixr
```

Start everything:

```bash
docker compose up -d
```

Your application service sends requests to `http://ixr:8080/v1` — no code changes needed beyond the base URL.

---

## 6. Point an existing client at ixr

**Python (openai SDK):**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="not-checked",
)

response = client.chat.completions.create(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

**Node.js:**

```javascript
import OpenAI from "openai";
const client = new OpenAI({ baseURL: "http://localhost:8080/v1", apiKey: "x" });
const resp = await client.chat.completions.create({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "Hello!" }],
});
console.log(resp.choices[0].message.content);
```

---

## 7. Run the interactive demo

The demo runs on your host against the container — no changes to the container needed.

```bash
# Get the demo script (demo_test branch)
git clone --branch demo_test https://github.com/YashVishwas/ixr.git ixr-demo

# Container must already be running (step 2 or 3)
python3 ixr-demo/demo/run_demo.py --port 8080 --branch phase-2_2
```

---

## Useful Docker commands

```bash
# stop and remove
docker stop ixr && docker rm ixr

# restart
docker restart ixr

# run on a different port (host 8080 → container 8080)
docker run -d --name ixr -p 8080:8080 -e ANTHROPIC_API_KEY="..." ixr

# override config port inside the container
docker run -d --name ixr -p 8081:8081 \
  -e ANTHROPIC_API_KEY="..." \
  ixr --port 8081
```

---

## Troubleshooting

**Container exits immediately**
```bash
docker logs ixr
```
Most common cause: no API keys were passed, so ixr exits with "no providers configured".

**"port is already allocated"**
Change the host port: `-p 8081:8080`

**Apple Silicon — image not found for arm64**
If you built from source with `docker build`, the Dockerfile targets `linux/amd64` by default. Docker Desktop on Apple Silicon transparently runs this via Rosetta. To build a native arm64 image:

```bash
docker buildx build --platform linux/arm64 -t ixr-arm64 --load .
docker run -d --name ixr -p 8080:8080 -e ANTHROPIC_API_KEY="..." ixr-arm64
```

Or just use the ghcr.io image which is already multi-arch.
