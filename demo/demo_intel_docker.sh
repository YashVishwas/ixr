#!/usr/bin/env bash
# demo_intel_docker.sh — Docker-based ixr demo for Mac Intel (linux/amd64).
#
# Usage:  ./demo/demo_intel_docker.sh [branch-name]
#
# Requires: git, docker, python3 (stdlib only)
# API keys: set at least one of GROQ_API_KEY, CEREBRAS_API_KEY, MISTRAL_API_KEY,
#           SAMBANOVA_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY

set -euo pipefail

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
DEMO_DIR="$REPO_ROOT/demo"
PORT=8086
WORKTREE_BASE="/tmp/ixr-demo-intel-docker"
CONTAINER_NAME="ixr-demo-intel"
IMAGE_TAG="ixr-demo:intel"

bold()  { printf '\033[1m%s\033[0m' "$*"; }
cyan()  { printf '\033[1;36m%s\033[0m' "$*"; }
green() { printf '\033[1;32m%s\033[0m' "$*"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$*"; }
red()   { printf '\033[1;31m%s\033[0m' "$*"; }

check_platform() {
  local arch; arch="$(uname -m)"
  if [[ "$arch" != "x86_64" ]]; then
    echo ""; echo "  $(yellow "Warning: detected architecture '$arch', expected x86_64.")"
    echo "  $(yellow "This script targets Mac Intel. Use demo_silicon_docker.sh on Apple Silicon.")"
    echo ""; printf "  Continue anyway? [y/N]: "; read -r ans
    [[ "$ans" =~ ^[Yy]$ ]] || exit 0
  fi
}

check_docker() {
  if ! command -v docker &>/dev/null; then
    echo "  $(red 'Error: docker not found. Install Docker Desktop from https://docker.com')"; exit 1
  fi
  if ! docker info &>/dev/null; then
    echo "  $(red 'Error: Docker daemon is not running. Start Docker Desktop and try again.')"; exit 1
  fi
}

cleanup() {
  docker stop "$CONTAINER_NAME" 2>/dev/null || true
  docker rm   "$CONTAINER_NAME" 2>/dev/null || true
  if [[ -d "$WORKTREE_BASE" ]] && [[ "$WORKTREE_BASE" != "$REPO_ROOT" ]]; then
    echo "  Removing worktree $WORKTREE_BASE..."
    git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_BASE" 2>/dev/null || rm -rf "$WORKTREE_BASE"
  fi
}
trap cleanup EXIT INT TERM

print_branch_info() {
  echo ""
  echo "  $(bold 'Available branches and their features:')"
  echo ""
  echo "  $(cyan 'main')         — OpenAI-compatible proxy, 12 provider adapters, auto-routing catalog"
  echo "  $(cyan 'phase-2')      — +circuit breaker, intent parser, scoring engine, rate limiting, routing filter"
  echo "  $(cyan 'phase-2_2')    — +shadow testing, streaming (SSE), telemetry plugin, config hot-reload,"
  echo "                     secrets, tenant management, executor with retry/fallback"
  echo "  $(cyan 'phase-2_3')    — +observability (OTEL traces, Prometheus metrics, request ID), semantic cache,"
  echo "                     Bedrock/Ollama/llama.cpp/local providers, embeddings + images endpoints,"
  echo "                     full tool-calling spec, bus adapters, schema registry"
  echo ""
  echo "  $(yellow 'Note:') each branch is a superset of the previous."
  echo ""
}

select_branch() {
  local -a branches=()
  while IFS= read -r line; do
    branch="${line#  }"; branch="${branch# }"; branch="${branch#\* }"
    [[ -n "$branch" ]] && branches+=("$branch")
  done < <(git -C "$REPO_ROOT" branch 2>/dev/null)
  while IFS= read -r line; do
    ref="${line#  remotes/origin/}"; ref="${ref# }"
    [[ "$ref" == HEAD* ]] && continue
    already=0
    for b in "${branches[@]}"; do [[ "$b" == "$ref" ]] && already=1; done
    [[ $already -eq 0 ]] && branches+=("$ref")
  done < <(git -C "$REPO_ROOT" branch -r 2>/dev/null)
  echo "  $(bold 'Select a branch to demo:')"; echo ""
  local i=1
  for b in "${branches[@]}"; do printf "    %2d)  %s\n" "$i" "$b"; i=$((i+1)); done
  echo ""; printf "  Enter number [default: main, q to quit]: "; read -r choice; choice="${choice:-}"
  if [[ "$choice" == "q" || "$choice" == "Q" || "$choice" == "quit" ]]; then
    echo ""; echo "  $(green 'Bye.')"; exit 0
  elif [[ -z "$choice" ]]; then BRANCH="main"
  elif [[ "$choice" =~ ^[0-9]+$ ]] && [[ "$choice" -ge 1 ]] && [[ "$choice" -le "${#branches[@]}" ]]; then
    BRANCH="${branches[$((choice-1))]}"
  else BRANCH="main"; fi
}

setup_worktree() {
  echo ""; echo "  $(bold "Setting up worktree for branch:") $(cyan "$BRANCH")"
  local current_branch
  current_branch=$(git -C "$REPO_ROOT" branch --show-current 2>/dev/null || echo "")
  if [[ "$current_branch" == "$BRANCH" ]]; then
    echo "  Branch is the current worktree — running from $REPO_ROOT"; WORKTREE_BASE="$REPO_ROOT"; return
  fi
  if [[ -d "$WORKTREE_BASE" ]]; then
    git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_BASE" 2>/dev/null || rm -rf "$WORKTREE_BASE"
  fi
  local ref="$BRANCH"
  if ! git -C "$REPO_ROOT" rev-parse --verify "$BRANCH" &>/dev/null; then ref="origin/$BRANCH"; fi
  git -C "$REPO_ROOT" worktree add "$WORKTREE_BASE" "$ref" 2>&1 | sed 's/^/    /'
  echo "  Worktree ready at $WORKTREE_BASE"
}

build_ixr() {
  echo ""; echo "  $(bold 'Building Docker image (linux/amd64)...')"
  docker build --platform linux/amd64 -t "$IMAGE_TAG" "$WORKTREE_BASE" 2>&1 | sed 's/^/    /'
  echo "  $(green 'Image built.')"
}

write_demo_config() {
  cat > "$WORKTREE_BASE/demo-ixr.yaml" <<YAML
server:
  port: $PORT

log_level: warn

auth:
  disable_auth: true

providers:
  openai:
    api_key: "${OPENAI_API_KEY:-}"
  anthropic:
    api_key: "${ANTHROPIC_API_KEY:-}"
  gemini:
    api_key: "${GOOGLE_API_KEY:-}"
  gemma:
    api_key: "${GOOGLE_API_KEY:-}"
  llama:
    api_key: "${GROQ_API_KEY:-}"
  deepseek:
    api_key: "${DEEPSEEK_API_KEY:-}"
  cerebras:
    api_key: "${CEREBRAS_API_KEY:-}"
  mistral:
    api_key: "${MISTRAL_API_KEY:-}"
  openrouter:
    api_key: "${OPENROUTER_API_KEY:-}"
  sambanova:
    api_key: "${SAMBANOVA_API_KEY:-}"
  github:
    api_key: "${GITHUB_TOKEN:-}"
  zhipu:
    api_key: "${ZHIPU_API_KEY:-}"

chains:
  fast-refine:
    models:
      - llama-3.3-70b-versatile
      - mistral-small-latest
    prompts:
      - ""
      - "Improve the previous answer: fix any inaccuracies and make it more concise."
  smart-qa:
    models:
      - llama-3.3-70b-versatile
      - gpt-oss-120b
    prompts:
      - ""
      - "Review the previous answer. Address any gaps, uncertainties, or errors. Provide a final, improved response."
  debate:
    models:
      - llama-3.3-70b-versatile
      - mistral-small-latest
      - gpt-oss-120b
    prompts:
      - ""
      - "Consider the previous answer critically. Offer a different perspective or correct any mistakes."
      - "Synthesize the two perspectives above into the best possible answer."
YAML
}

start_server() {
  echo ""; echo "  $(bold "Starting ixr container on port ${PORT}...")"
  docker run -d \
    --name "$CONTAINER_NAME" \
    --platform linux/amd64 \
    -p "$PORT:$PORT" \
    -v "$WORKTREE_BASE/demo-ixr.yaml:/demo-ixr.yaml:ro" \
    "$IMAGE_TAG" \
    -config /demo-ixr.yaml -port "$PORT" \
    > /dev/null

  local attempts=0
  until curl -sf "http://localhost:$PORT/v1/chat/completions" \
        -X POST -H "Content-Type: application/json" \
        -d '{"model":"_probe_","messages":[]}' &>/dev/null \
     || [[ $attempts -ge 20 ]]; do
    sleep 0.5; ((attempts++))
    if ! docker inspect "$CONTAINER_NAME" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
      echo "  $(red 'Container failed to start. Logs:')"; docker logs "$CONTAINER_NAME" 2>&1 | sed 's/^/    /'; exit 1
    fi
  done
  echo "  $(green "ixr running in Docker (${CONTAINER_NAME}) -> http://localhost:${PORT}")"
}

echo ""
echo "  $(bold '╔══════════════════════════════════════════════╗')"
echo "  $(bold '║   ixr  —  interactive demo (Intel · Docker)  ║')"
echo "  $(bold '╚══════════════════════════════════════════════╝')"

check_platform; check_docker; print_branch_info

if [[ $# -ge 1 ]]; then BRANCH="$1"; echo "  Using branch: $(cyan "$BRANCH")"; else select_branch; fi

setup_worktree; build_ixr; write_demo_config; start_server

echo ""; echo "  $(bold '─────────────────────────────────────────────')"; echo "  $(bold '  Running demo scenarios...')"; echo "  $(bold '─────────────────────────────────────────────')"
python3 "$DEMO_DIR/run_demo.py" --port "$PORT" --branch "$BRANCH" --log /dev/null
echo ""; echo "  $(green 'Demo complete.')"; echo "  Container logs: docker logs $CONTAINER_NAME"; echo ""
