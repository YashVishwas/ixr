#!/usr/bin/env bash
# demo_deploy.sh — Run the ixr interactive demo via binary, docker, or go-embed.
#
# Usage:  ./demo/demo_deploy.sh [binary|docker|embed]
#
# Requires: go >=1.21 (binary/embed), docker (docker mode)
# API keys: set at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, GROQ_API_KEY,
#           CEREBRAS_API_KEY, MISTRAL_API_KEY, SAMBANOVA_API_KEY, GITHUB_TOKEN

set -euo pipefail

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
DEMO_DIR="$REPO_ROOT/demo"
PORT=8080
CONTAINER_NAME="ixr-demo"
SERVER_PID=""
LOG_FILE="/tmp/ixr-demo-deploy.log"

bold()  { printf '\033[1m%s\033[0m' "$*"; }
cyan()  { printf '\033[1;36m%s\033[0m' "$*"; }
green() { printf '\033[1;32m%s\033[0m' "$*"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$*"; }
red()   { printf '\033[1;31m%s\033[0m' "$*"; }

cleanup() {
  echo ""
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "  Stopping ixr (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
  fi
  if docker ps -q --filter "name=^/${CONTAINER_NAME}$" 2>/dev/null | grep -q .; then
    echo "  Stopping Docker container..."
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
    docker rm   "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

# ── Mode selection ─────────────────────────────────────────────────────────────

select_mode() {
  echo ""
  echo "  $(bold 'Select deployment mode:')"
  echo ""
  echo "    $(cyan '1')  binary   — make dist → run darwin/arm64 binary"
  echo "    $(cyan '2')  docker   — docker build → run container"
  echo "    $(cyan '3')  embed    — go run ./cmd/ixr  (go-embed path)"
  echo ""
  printf "  Enter number [default: 1]: "
  read -r choice
  case "${choice:-1}" in
    2) MODE="docker" ;;
    3) MODE="embed"  ;;
    *) MODE="binary" ;;
  esac
}

# ── Server startup ─────────────────────────────────────────────────────────────

start_binary() {
  echo ""
  echo "  $(bold 'Building binary (darwin/arm64)...')"
  (cd "$REPO_ROOT" && make dist 2>&1) | sed 's/^/    /'
  echo ""
  echo "  $(bold 'Starting ixr binary on port')" "$(cyan "$PORT")..."
  "$REPO_ROOT/dist/ixr-darwin-amd64" -port "$PORT" >"$LOG_FILE" 2>&1 &
  SERVER_PID=$!
}

start_docker() {
  # Stop any stale container from a previous run
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true

  echo ""
  echo "  $(bold 'Building Docker image...')"
  docker build -t ixr "$REPO_ROOT" 2>&1 | sed 's/^/    /'

  # Collect whichever API key env vars are set
  local env_flags=()
  local keys=(ANTHROPIC_API_KEY OPENAI_API_KEY GOOGLE_API_KEY GROQ_API_KEY
              CEREBRAS_API_KEY MISTRAL_API_KEY SAMBANOVA_API_KEY
              OPENROUTER_API_KEY GITHUB_TOKEN ZHIPU_API_KEY DEEPSEEK_API_KEY)
  for k in "${keys[@]}"; do
    [[ -n "${!k:-}" ]] && env_flags+=(-e "$k=${!k}")
  done

  echo ""
  echo "  $(bold 'Starting ixr container on port')" "$(cyan "$PORT")..."
  docker run -d \
    --name "$CONTAINER_NAME" \
    -p "$PORT:$PORT" \
    "${env_flags[@]}" \
    ixr >"$LOG_FILE" 2>&1
}

start_embed() {
  echo ""
  echo "  $(bold 'Starting ixr via go run (embed path) on port')" "$(cyan "$PORT")..."
  (cd "$REPO_ROOT" && go run ./cmd/ixr -port "$PORT") >"$LOG_FILE" 2>&1 &
  SERVER_PID=$!
}

# ── Health check ───────────────────────────────────────────────────────────────

wait_for_server() {
  local attempts=0
  printf "  Waiting for ixr to be ready"
  until curl -s -o /dev/null "http://localhost:$PORT/v1/chat/completions" \
        -X POST -H "Content-Type: application/json" \
        -d '{"model":"_probe_","messages":[]}' \
     || [[ $attempts -ge 30 ]]; do
    printf "."
    sleep 0.5
    attempts=$((attempts + 1))
  done
  echo ""

  if [[ $attempts -ge 30 ]]; then
    echo "  $(red 'Server did not start in time. Logs:')"
    if [[ "$MODE" == "docker" ]]; then
      docker logs "$CONTAINER_NAME" 2>&1 | sed 's/^/    /'
    else
      cat "$LOG_FILE" | sed 's/^/    /'
    fi
    exit 1
  fi

  echo "  $(green "ixr ready -> http://localhost:${PORT}")"
}

# ── Main ───────────────────────────────────────────────────────────────────────

echo ""
echo "  $(bold '╔══════════════════════════════════════════╗')"
echo "  $(bold '║      ixr  —  cross-compile demo          ║')"
echo "  $(bold '╚══════════════════════════════════════════╝')"

if [[ $# -ge 1 ]]; then
  MODE="$1"
  echo "  Mode: $(cyan "$MODE")"
else
  select_mode
  echo "  Mode: $(cyan "$MODE")"
fi

case "$MODE" in
  binary) start_binary ;;
  docker) start_docker ;;
  embed)  start_embed  ;;
  *)
    echo "  $(red "Unknown mode: $MODE")  (use binary, docker, or embed)"
    exit 1
    ;;
esac

wait_for_server

echo ""
echo "  $(bold '─────────────────────────────────────────────')"
echo "  $(bold '  Running demo scenarios...')"
echo "  $(bold '─────────────────────────────────────────────')"

BRANCH="$(git -C "$REPO_ROOT" branch --show-current 2>/dev/null || echo 'demo_cross_compile')"
python3 "$DEMO_DIR/run_demo.py" --port "$PORT" --branch "$BRANCH" --log "$LOG_FILE"

echo ""
echo "  $(green 'Demo complete.')"
echo "  Server log: $LOG_FILE"
echo ""
