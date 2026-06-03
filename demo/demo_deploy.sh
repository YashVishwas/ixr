#!/usr/bin/env bash
# demo_deploy.sh — Run the ixr interactive demo via binary, docker, or go-embed.
#
# Usage:  ./demo/demo_deploy.sh [binary|docker|docker-amd64|docker-arm64|embed]
#
# Everything runs in ONE terminal — no second terminal needed.
# API keys are forwarded automatically from your shell to the server and demo.
#
# Requires: go >=1.21 (binary/embed), docker (docker modes)
# API keys: export at least one before running, e.g.:
#           export ANTHROPIC_API_KEY="sk-ant-..."

set -euo pipefail

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
DEMO_DIR="$REPO_ROOT/demo"
PORT=8080
CONTAINER_NAME="ixr-demo"
SERVER_PID=""
LOG_FILE="/tmp/ixr-demo-deploy.log"
ENV_FLAGS=()

bold()  { printf '\033[1m%s\033[0m' "$*"; }
cyan()  { printf '\033[1;36m%s\033[0m' "$*"; }
green() { printf '\033[1;32m%s\033[0m' "$*"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$*"; }
red()   { printf '\033[1;31m%s\033[0m' "$*"; }
dim()   { printf '\033[2m%s\033[0m' "$*"; }

# ── Cleanup ────────────────────────────────────────────────────────────────────

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

# ── Preflight checks ───────────────────────────────────────────────────────────

check_api_keys() {
  local keys=(ANTHROPIC_API_KEY OPENAI_API_KEY GOOGLE_API_KEY GROQ_API_KEY
              CEREBRAS_API_KEY MISTRAL_API_KEY SAMBANOVA_API_KEY
              OPENROUTER_API_KEY GITHUB_TOKEN ZHIPU_API_KEY DEEPSEEK_API_KEY)
  local found=0
  for k in "${keys[@]}"; do
    [[ -n "${!k:-}" ]] && found=1 && break
  done
  if [[ $found -eq 0 ]]; then
    echo ""
    echo "  $(red 'No API keys found in this shell.')"
    echo "  Export at least one before running, e.g.:"
    echo "    $(cyan 'export ANTHROPIC_API_KEY="sk-ant-..."')"
    echo ""
    exit 1
  fi
}

check_port() {
  if lsof -ti ":$PORT" &>/dev/null 2>&1; then
    echo ""
    echo "  $(yellow "Port $PORT is already in use.")"
    printf "  Kill the existing process and continue? [Y/n]: "
    read -r ans
    if [[ "${ans:-Y}" =~ ^[Yy] ]]; then
      lsof -ti ":$PORT" | xargs kill -9 2>/dev/null || true
      sleep 0.5
      echo "  $(green 'Port cleared.')"
    else
      echo "  $(red "Exiting — free port $PORT and try again.")"
      exit 1
    fi
  fi
}

check_docker() {
  if ! docker info &>/dev/null 2>&1; then
    # Newer Docker Desktop for Mac uses a non-standard socket path
    if [[ -S "$HOME/.docker/desktop/docker.sock" ]]; then
      export DOCKER_HOST="unix://$HOME/.docker/desktop/docker.sock"
    fi
    if ! docker info &>/dev/null 2>&1; then
      echo ""
      echo "  $(red 'Docker is not running or not accessible.')"
      echo "  Start Docker Desktop and try again."
      exit 1
    fi
  fi
}

build_env_flags() {
  ENV_FLAGS=()
  local keys=(ANTHROPIC_API_KEY OPENAI_API_KEY GOOGLE_API_KEY GROQ_API_KEY
              CEREBRAS_API_KEY MISTRAL_API_KEY SAMBANOVA_API_KEY
              OPENROUTER_API_KEY GITHUB_TOKEN ZHIPU_API_KEY DEEPSEEK_API_KEY)
  for k in "${keys[@]}"; do
    [[ -n "${!k:-}" ]] && ENV_FLAGS+=(-e "$k=${!k}")
  done
}

# ── Mode selection ─────────────────────────────────────────────────────────────

select_mode() {
  echo ""
  echo "  $(bold 'Select deployment mode:')"
  echo ""
  echo "    $(cyan '1')  binary        — native macOS binary $(dim '(auto-detects arm64 / amd64)')"
  echo "    $(cyan '2')  docker        — Linux container, host architecture  $(dim '[fastest]')"
  echo "    $(cyan '3')  docker-amd64  — Linux container, force linux/amd64  $(dim '[test Intel Linux]')"
  echo "    $(cyan '4')  docker-arm64  — Linux container, force linux/arm64  $(dim '[test ARM Linux]')"
  echo "    $(cyan '5')  embed         — go run ./cmd/ixr  $(dim '[go-embed path]')"
  echo ""
  printf "  Enter number [default: 1]: "
  read -r choice
  case "${choice:-1}" in
    2) MODE="docker"        ;;
    3) MODE="docker-amd64"  ;;
    4) MODE="docker-arm64"  ;;
    5) MODE="embed"         ;;
    *) MODE="binary"        ;;
  esac
}

# ── Server startup ─────────────────────────────────────────────────────────────

start_binary() {
  check_port
  echo ""
  echo "  $(bold 'Building binaries (all platforms)...')"
  (cd "$REPO_ROOT" && make dist 2>&1) | sed 's/^/    /'

  local arch binary
  arch="$(uname -m)"
  if [[ "$arch" == "arm64" ]]; then
    binary="$REPO_ROOT/dist/ixr-darwin-arm64"
    echo ""
    echo "  $(green 'Apple Silicon detected — running darwin/arm64.')"
  else
    binary="$REPO_ROOT/dist/ixr-darwin-amd64"
    echo ""
    echo "  $(green 'Intel Mac detected — running darwin/amd64.')"
  fi
  echo "  $(dim 'Linux + Windows binaries are in dist/ — copy to their OS to run.')"
  echo "  $(dim 'Use docker-amd64 / docker-arm64 to test Linux locally.')"

  echo ""
  echo "  $(bold 'Starting ixr on port')" "$(cyan "$PORT")..."
  "$binary" -port "$PORT" >"$LOG_FILE" 2>&1 &
  SERVER_PID=$!
}

start_docker() {
  check_docker
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  build_env_flags
  check_port

  echo ""
  echo "  $(bold 'Building Docker image (host architecture)...')"
  docker build -t ixr "$REPO_ROOT" 2>&1 | sed 's/^/    /'

  echo ""
  echo "  $(bold 'Starting ixr container on port')" "$(cyan "$PORT")..."
  docker run -d \
    --name "$CONTAINER_NAME" \
    -p "$PORT:$PORT" \
    "${ENV_FLAGS[@]}" \
    ixr >"$LOG_FILE" 2>&1
}

start_docker_platform() {
  local platform="$1"
  local tag="ixr-${platform//\//-}"
  check_docker
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  build_env_flags
  check_port

  echo ""
  echo "  $(bold "Building Docker image for $platform (buildx)...")"
  docker buildx build --platform "$platform" -t "$tag" --load "$REPO_ROOT" 2>&1 | sed 's/^/    /'

  echo ""
  echo "  $(bold "Starting $platform container on port")" "$(cyan "$PORT")..."
  docker run -d \
    --name "$CONTAINER_NAME" \
    -p "$PORT:$PORT" \
    "${ENV_FLAGS[@]}" \
    "$tag" >"$LOG_FILE" 2>&1
}

start_embed() {
  check_port
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
    # catch a Docker container that exits immediately
    if [[ "$MODE" == docker* ]] && \
       ! docker ps -q --filter "name=^/${CONTAINER_NAME}$" 2>/dev/null | grep -q .; then
      echo ""
      echo "  $(red 'Container exited unexpectedly. Logs:')"
      docker logs "$CONTAINER_NAME" 2>&1 | sed 's/^/    /'
      exit 1
    fi
    printf "."
    sleep 0.5
    attempts=$((attempts + 1))
  done
  echo ""

  if [[ $attempts -ge 30 ]]; then
    echo "  $(red 'Server did not start in time. Logs:')"
    if [[ "$MODE" == docker* ]]; then
      docker logs "$CONTAINER_NAME" 2>&1 | sed 's/^/    /'
    else
      cat "$LOG_FILE" 2>/dev/null | sed 's/^/    /'
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
echo ""
echo "  $(dim 'Everything runs in this terminal — no second terminal needed.')"
echo "  $(dim 'API keys are forwarded automatically from your shell to the server and demo.')"

check_api_keys

if [[ $# -ge 1 ]]; then
  MODE="$1"
  echo ""
  echo "  Mode: $(cyan "$MODE")"
else
  select_mode
  echo "  Mode: $(cyan "$MODE")"
fi

case "$MODE" in
  binary)       start_binary ;;
  docker)       start_docker ;;
  docker-amd64) start_docker_platform "linux/amd64" ;;
  docker-arm64) start_docker_platform "linux/arm64" ;;
  embed)        start_embed  ;;
  *)
    echo "  $(red "Unknown mode: $MODE")"
    echo "  Valid modes: binary, docker, docker-amd64, docker-arm64, embed"
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
