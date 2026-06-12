#!/usr/bin/env bash
# demo_intel_embed.sh — Go-embed ixr demo for Mac Intel.
# ixr runs as a goroutine inside a host Go application — same process, same binary.
#
# Usage:  ./demo/demo_intel_embed.sh [branch-name]
#
# Requires: git, go >=1.21, python3 (stdlib only)
# API keys: set at least one of GROQ_API_KEY, CEREBRAS_API_KEY, MISTRAL_API_KEY,
#           SAMBANOVA_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY

set -euo pipefail

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
DEMO_DIR="$REPO_ROOT/demo"
PORT=8090
WORKTREE_BASE="/tmp/ixr-demo-intel-embed"
SERVER_PID=""
EMBED_APP_DIR=""

bold()  { printf '\033[1m%s\033[0m' "$*"; }
cyan()  { printf '\033[1;36m%s\033[0m' "$*"; }
green() { printf '\033[1;32m%s\033[0m' "$*"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$*"; }
red()   { printf '\033[1;31m%s\033[0m' "$*"; }

check_platform() {
  local arch; arch="$(uname -m)"
  if [[ "$arch" != "x86_64" ]]; then
    echo ""; echo "  $(yellow "Warning: detected '$arch', expected x86_64 (Mac Intel).")"
    echo "  $(yellow "Use demo_silicon_embed.sh on Apple Silicon.")"
    echo ""; printf "  Continue anyway? [y/N]: "; read -r ans
    [[ "$ans" =~ ^[Yy]$ ]] || exit 0
  fi
}

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo ""; echo "  Stopping embed server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "$EMBED_APP_DIR" ]] && [[ -d "$EMBED_APP_DIR" ]]; then rm -rf "$EMBED_APP_DIR"; fi
  rm -f "$WORKTREE_BASE/embed-bin" 2>/dev/null || true
  if [[ -d "$WORKTREE_BASE" ]] && [[ "$WORKTREE_BASE" != "$REPO_ROOT" ]]; then
    echo "  Removing worktree $WORKTREE_BASE..."
    git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_BASE" 2>/dev/null || rm -rf "$WORKTREE_BASE"
  fi
}
trap cleanup EXIT INT TERM

print_branch_info() {
  echo ""; echo "  $(bold 'Available branches and their features:')"; echo ""
  echo "  $(cyan 'main')         — OpenAI-compatible proxy, 12 provider adapters, auto-routing catalog"
  echo "  $(cyan 'phase-2')      — +circuit breaker, intent parser, scoring engine, rate limiting, routing filter"
  echo "  $(cyan 'phase-2_2')    — +shadow testing, streaming (SSE), telemetry plugin, config hot-reload,"
  echo "                     secrets, tenant management, executor with retry/fallback"
  echo "  $(cyan 'phase-2_3')    — +observability (OTEL traces, Prometheus metrics, request ID), semantic cache,"
  echo "                     Bedrock/Ollama/llama.cpp/local providers, embeddings + images endpoints,"
  echo "                     full tool-calling spec, bus adapters, schema registry"
  echo ""; echo "  $(yellow 'Note:') each branch is a superset of the previous."; echo ""
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
    already=0; for b in "${branches[@]}"; do [[ "$b" == "$ref" ]] && already=1; done
    [[ $already -eq 0 ]] && branches+=("$ref")
  done < <(git -C "$REPO_ROOT" branch -r 2>/dev/null)
  echo "  $(bold 'Select a branch to demo:')"; echo ""
  local i=1; for b in "${branches[@]}"; do printf "    %2d)  %s\n" "$i" "$b"; i=$((i+1)); done
  echo ""; printf "  Enter number [default: main, q to quit]: "; read -r choice; choice="${choice:-}"
  if [[ "$choice" == "q" || "$choice" == "Q" || "$choice" == "quit" ]]; then echo ""; echo "  $(green 'Bye.')"; exit 0
  elif [[ -z "$choice" ]]; then BRANCH="main"
  elif [[ "$choice" =~ ^[0-9]+$ ]] && [[ "$choice" -ge 1 ]] && [[ "$choice" -le "${#branches[@]}" ]]; then BRANCH="${branches[$((choice-1))]}"
  else BRANCH="main"; fi
}

setup_worktree() {
  echo ""; echo "  $(bold "Setting up worktree for branch:") $(cyan "$BRANCH")"
  local current_branch; current_branch=$(git -C "$REPO_ROOT" branch --show-current 2>/dev/null || echo "")
  if [[ "$current_branch" == "$BRANCH" ]]; then
    echo "  Branch is the current worktree — running from $REPO_ROOT"
    WORKTREE_BASE="$REPO_ROOT"; EMBED_APP_DIR="$REPO_ROOT/cmd/demo-embed"; return
  fi
  if [[ -d "$WORKTREE_BASE" ]]; then git -C "$REPO_ROOT" worktree remove --force "$WORKTREE_BASE" 2>/dev/null || rm -rf "$WORKTREE_BASE"; fi
  local ref="$BRANCH"
  if ! git -C "$REPO_ROOT" rev-parse --verify "$BRANCH" &>/dev/null; then ref="origin/$BRANCH"; fi
  git -C "$REPO_ROOT" worktree add "$WORKTREE_BASE" "$ref" 2>&1 | sed 's/^/    /'
  echo "  Worktree ready at $WORKTREE_BASE"; EMBED_APP_DIR="$WORKTREE_BASE/cmd/demo-embed"
}

write_embed_app() {
  mkdir -p "$EMBED_APP_DIR"
  cat > "$EMBED_APP_DIR/main.go" << 'GOEOF'
// demo-embed: shows ixr running inside a host Go service — same binary, same process.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	ixr "github.com/YashVishwas/ixr/pkg/ixr"
)

func main() {
	port, _ := strconv.Atoi(os.Getenv("IXR_PORT"))
	if port == 0 {
		port = 8090
	}
	configFile := os.Getenv("IXR_CONFIG_FILE")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host app healthy — ixr embedded on :%d\n", port)
	})
	go func() {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			log.Printf("host app (optional): %v", err)
			return
		}
		appPort := ln.Addr().(*net.TCPAddr).Port
		log.Printf("host app on :%d — ixr embedded on :%d\n", appPort, port)
		http.Serve(ln, mux)
	}()

	time.Sleep(100 * time.Millisecond)

	if err := ixr.Start(
		ixr.WithPort(port),
		ixr.WithConfigFile(configFile),
	); err != nil {
		log.Fatalf("ixr: %v", err)
	}
}
GOEOF
}

build_ixr() {
  echo ""; echo "  $(bold 'Building embed demo (darwin/amd64)...')"
  write_embed_app
  (cd "$WORKTREE_BASE" && GOOS=darwin GOARCH=amd64 go build -o "$WORKTREE_BASE/embed-bin" ./cmd/demo-embed/) 2>&1 | sed 's/^/    /'
  echo "  $(green 'Build complete.')"
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
    api_key: \${OPENAI_API_KEY}
  anthropic:
    api_key: \${ANTHROPIC_API_KEY}
  gemini:
    api_key: \${GOOGLE_API_KEY}
  gemma:
    api_key: \${GOOGLE_API_KEY}
  llama:
    api_key: \${GROQ_API_KEY}
  deepseek:
    api_key: \${DEEPSEEK_API_KEY}
  cerebras:
    api_key: \${CEREBRAS_API_KEY}
  mistral:
    api_key: \${MISTRAL_API_KEY}
  openrouter:
    api_key: \${OPENROUTER_API_KEY}
  sambanova:
    api_key: \${SAMBANOVA_API_KEY}
  github:
    api_key: \${GITHUB_TOKEN}
  zhipu:
    api_key: \${ZHIPU_API_KEY}
chains:
  fast-refine:
    models: [llama-3.3-70b-versatile, mistral-small-latest]
    prompts: ["", "Improve the previous answer: fix any inaccuracies and make it more concise."]
  smart-qa:
    models: [llama-3.3-70b-versatile, gpt-oss-120b]
    prompts: ["", "Review the previous answer. Address any gaps, uncertainties, or errors."]
  debate:
    models: [llama-3.3-70b-versatile, mistral-small-latest, gpt-oss-120b]
    prompts: ["", "Consider the previous answer critically. Offer a different perspective.", "Synthesize the two perspectives above into the best possible answer."]
YAML
}

start_server() {
  echo ""; echo "  $(bold "Starting embed server on port ${PORT}...")"
  IXR_PORT="$PORT" IXR_CONFIG_FILE="$WORKTREE_BASE/demo-ixr.yaml" \
    "$WORKTREE_BASE/embed-bin" > "$WORKTREE_BASE/ixr.log" 2>&1 &
  SERVER_PID=$!
  local attempts=0
  until curl -sf "http://localhost:$PORT/v1/chat/completions" \
        -X POST -H "Content-Type: application/json" \
        -d '{"model":"_probe_","messages":[]}' &>/dev/null \
     || [[ $attempts -ge 20 ]]; do
    sleep 0.5; ((attempts++))
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "  $(red 'Server failed to start. Logs:')"; cat "$WORKTREE_BASE/ixr.log" | sed 's/^/    /'; exit 1
    fi
  done
  echo "  $(green "ixr embedded (PID ${SERVER_PID}) -> http://localhost:${PORT}")"
}

echo ""; echo "  $(bold '╔══════════════════════════════════════════════╗')"; echo "  $(bold '║   ixr  —  interactive demo (Intel · Embed)   ║')"; echo "  $(bold '╚══════════════════════════════════════════════╝')"
echo "  $(cyan 'Embed mode:') ixr runs as a goroutine inside the host app binary."; echo ""
check_platform; print_branch_info
if [[ $# -ge 1 ]]; then BRANCH="$1"; echo "  Using branch: $(cyan "$BRANCH")"; else select_branch; fi
setup_worktree; build_ixr; write_demo_config; start_server
echo ""; echo "  $(bold '─────────────────────────────────────────────')"; echo "  $(bold '  Running demo scenarios...')"; echo "  $(bold '─────────────────────────────────────────────')"
python3 "$DEMO_DIR/run_demo.py" --port "$PORT" --branch "$BRANCH" --log "$WORKTREE_BASE/ixr.log"
echo ""; echo "  $(green 'Demo complete.')"; echo "  Server log: $WORKTREE_BASE/ixr.log"; echo ""
