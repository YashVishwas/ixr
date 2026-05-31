#!/usr/bin/env python3
"""
run_demo.py — ixr demo scenario runner.

Makes a series of HTTP calls against a running ixr proxy and prints the
results with commentary explaining what each scenario demonstrates.

Usage (called from demo.sh):
    python3 run_demo.py --port 7001 --branch phase-2_2
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request

# ── ANSI helpers ──────────────────────────────────────────────────────────────

def _c(code, text):
    return f"\033[{code}m{text}\033[0m"

def bold(t):    return _c("1", t)
def cyan(t):    return _c("1;36", t)
def green(t):   return _c("1;32", t)
def yellow(t):  return _c("1;33", t)
def red(t):     return _c("1;31", t)
def dim(t):     return _c("2", t)

# ── Branch feature registry ───────────────────────────────────────────────────

# What each branch has wired and available (cumulative).
BRANCH_FEATURES = {
    "main": {
        "basic_routing",
        "auto_routing",
        "event_bus",
    },
    "phase-2": {
        "basic_routing",
        "auto_routing",
        "event_bus",
        "circuit_breaker_domain",
        "intent_parser",
        "scoring_engine",
        "rate_limit_domain",
        "routing_filter_scorer",
    },
    "phase-2_2": {
        "basic_routing",
        "auto_routing",
        "event_bus",
        "circuit_breaker_domain",
        "intent_parser",
        "scoring_engine",
        "rate_limit_domain",
        "routing_filter_scorer",
        "shadow_routing",
        "streaming_sse",
        "telemetry_plugin",
        "config_hot_reload",
        "secrets_management",
        "tenant_management",
        "executor_retry_fallback",
        "redis_postgres_stores",
    },
}

def features_for(branch):
    b = branch.removeprefix("origin/").removeprefix("remotes/origin/")
    # If exact match fails, fall back to main feature set
    return BRANCH_FEATURES.get(b, BRANCH_FEATURES.get("main", set()))

# ── Provider detection ────────────────────────────────────────────────────────

# (env_var, ixr_model_name, label, free_tier)
FREE_PROVIDERS = [
    ("CEREBRAS_API_KEY",  "qwen3-8b",                   "Cerebras (qwen3-8b)",             True),
    ("GROQ_API_KEY",      "llama-3.1-8b-instant",       "Groq/Llama (llama-3.1-8b-instant)", True),
    ("MISTRAL_API_KEY",   "mistral-small-latest",        "Mistral (mistral-small-latest)",  True),
    ("SAMBANOVA_API_KEY", "Meta-Llama-3.1-8B-Instruct", "SambaNova (Meta-Llama-3.1-8B)",   True),
    ("GITHUB_TOKEN",      "openai/gpt-4.1-mini",        "GitHub Models (gpt-4.1-mini)",    True),
]

PAID_PROVIDERS = [
    ("OPENAI_API_KEY",    "gpt-4o-mini",                "OpenAI (gpt-4o-mini)",            False),
    ("ANTHROPIC_API_KEY", "claude-3-5-haiku-20241022",  "Anthropic (claude-3-5-haiku)",    False),
    ("GOOGLE_API_KEY",    "gemini-1.5-flash",           "Gemini (gemini-1.5-flash)",        False),
]

def detect_providers():
    available = []
    for entry in FREE_PROVIDERS + PAID_PROVIDERS:
        env_var = entry[0]
        if os.environ.get(env_var, "").strip():
            available.append(entry)
    return available

# ── HTTP helper ───────────────────────────────────────────────────────────────

def chat(port, payload, extra_headers=None, timeout=30):
    """POST /v1/chat/completions. Returns (response_dict, latency_sec, error_str)."""
    url = f"http://localhost:{port}/v1/chat/completions"
    body = json.dumps(payload).encode()
    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    for k, v in (extra_headers or {}).items():
        req.add_header(k, v)

    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            latency = time.monotonic() - t0
            data = json.loads(resp.read())
            return data, latency, None
    except urllib.error.HTTPError as e:
        latency = time.monotonic() - t0
        try:
            err_body = json.loads(e.read())
            msg = err_body.get("error", {}).get("message", str(e))
        except Exception:
            msg = str(e)
        return None, latency, f"HTTP {e.code}: {msg}"
    except Exception as e:
        latency = time.monotonic() - t0
        return None, latency, str(e)

# ── Display helpers ───────────────────────────────────────────────────────────

def section(title):
    print()
    print(f"  {bold(cyan('▶  ' + title))}")
    print(f"  {dim('─' * 56)}")

def print_result(resp, latency, error):
    if error:
        print(f"  {red('✗')}  Error: {error}")
        return
    if not resp:
        print(f"  {red('✗')}  No response received")
        return

    model   = resp.get("model", "?")
    choices = resp.get("choices", [])
    usage   = resp.get("usage", {})
    content = choices[0]["message"]["content"] if choices else "(no content)"
    t_in    = usage.get("prompt_tokens", "?")
    t_out   = usage.get("completion_tokens", "?")

    if len(content) > 200:
        content = content[:200] + "…"

    print(f"  {green('✓')}  Routed to: {bold(model)}")
    print(f"       Latency: {latency*1000:.0f} ms  |  Tokens in: {t_in}  out: {t_out}")
    print(f"       Response: {dim(content)}")

def note(msg):
    print(f"  {yellow('ℹ')}  {dim(msg)}")

# ── Scenarios ─────────────────────────────────────────────────────────────────

def scenario_basic(port, model, label):
    section("Scenario 1 — Basic routing (explicit model)")
    note(f"Sends a request to ixr specifying {label}.")
    note("ixr resolves the model prefix to the right provider and forwards the call.")
    print()
    print(f"  Request: model={bold(model)}")

    resp, lat, err = chat(port, {
        "model": model,
        "messages": [{"role": "user", "content": "Reply with exactly: ixr routing works!"}],
    })
    print_result(resp, lat, err)


def scenario_auto_routing(port):
    section("Scenario 2 — Auto-routing  (model=\"auto\")")
    note("ixr's scoring engine picks a model from its internal catalog.")
    note("X-IXR-Task=coding  X-IXR-Budget=2.0 → scorer maximises capability, penalises cost.")
    print()
    print(f"  Request: model={bold('auto')}  task=coding  budget=$2.00/1M")

    resp, lat, err = chat(port, {
        "model": "auto",
        "messages": [{"role": "user", "content": "What is 2+2?"}],
    }, extra_headers={
        "X-IXR-Task": "coding",
        "X-IXR-Budget": "2.0",
    })
    print_result(resp, lat, err)
    if err and "no provider" in (err or ""):
        note("The auto-selected catalog model has no configured provider key — add it to proceed.")
    if resp:
        note("ixr chose the catalog model with the highest coding score within the $2/1M cap.")


def scenario_tight_budget(port):
    section("Scenario 3 — Budget-constrained auto-routing")
    note("Budget $0.20/1M eliminates expensive models; scorer picks cheapest capable option.")
    print()
    print(f"  Request: model={bold('auto')}  task=reasoning  budget=$0.20/1M")

    resp, lat, err = chat(port, {
        "model": "auto",
        "messages": [{"role": "user", "content": "Explain recursion in one sentence."}],
    }, extra_headers={
        "X-IXR-Task": "reasoning",
        "X-IXR-Budget": "0.20",
    })
    print_result(resp, lat, err)


def scenario_latency(port):
    section("Scenario 4 — Latency-sensitive auto-routing")
    note("X-IXR-Latency: sensitive boosts the latency weight in the scoring formula (×1.5).")
    print()
    print(f"  Request: model={bold('auto')}  task=math  latency=sensitive")

    resp, lat, err = chat(port, {
        "model": "auto",
        "messages": [{"role": "user", "content": "Is 17 a prime number? Answer yes or no."}],
    }, extra_headers={
        "X-IXR-Task": "math",
        "X-IXR-Latency": "sensitive",
    })
    print_result(resp, lat, err)


def scenario_use_case(port, model):
    section("Scenario 5 — Use-case tagging  (X-IXR-UseCase)")
    note("Attaches a business label to every CallEvent published on the bus.")
    note("The audit-log and telemetry plugins read it for per-product observability.")
    print()
    print(f"  Request: model={bold(model)}  X-IXR-UseCase=demo-session")

    resp, lat, err = chat(port, {
        "model": model,
        "messages": [{"role": "user", "content": "Say: telemetry tagged."}],
    }, extra_headers={
        "X-IXR-UseCase": "demo-session",
    })
    print_result(resp, lat, err)
    if resp:
        note("Bus emitted: CallEvent{use_case_id: 'demo-session', provider: '...', latency_ms: ...}")


def scenario_shadow(port, primary_model, shadow_model, shadow_label):
    section("Scenario 6 — Shadow routing  (phase-2_2)")
    note("X-IXR-Shadow-Model sends the same prompt to a second model in the background.")
    note("The caller only gets the primary response; the shadow result goes to the event bus.")
    note("Use this to compare quality or cost before committing to a provider switch.")
    print()
    print(f"  Primary: {bold(primary_model)}")
    print(f"  Shadow:  {bold(shadow_model)}  ({shadow_label})  ← async, never blocks caller")
    print()
    print(f"  Request: model={bold(primary_model)}  X-IXR-Shadow-Model={bold(shadow_model)}")

    resp, lat, err = chat(port, {
        "model": primary_model,
        "messages": [{"role": "user", "content": "What color is the sky? One word."}],
    }, extra_headers={
        "X-IXR-Shadow-Model": shadow_model,
    })
    print_result(resp, lat, err)
    if resp:
        note("Shadow CallEvent carries ShadowMetadata{primary_id, primary_model, shadow_model}.")


def scenario_streaming_note():
    section("Scenario 7 — Streaming infrastructure  (phase-2_2)")
    note("phase-2_2 added StreamWriter (SSE) and the streamHandler skeleton.")
    note("Streaming is scaffolded and ready for provider-level wiring in phase 3.")
    note("Sending stream=true right now returns a descriptive 501:")
    print()
    print(f"    {dim('POST /v1/chat/completions  {\"stream\": true, ...}')}")
    print(f"    {yellow('← 501')}: streaming is not yet supported; set stream=false")


def scenario_domain_overview():
    section("Phase-2 domain packages (not yet wired to HTTP)")
    note("These pure-domain packages were built in phase-2 and are ready to wire up:")
    print()
    rows = [
        ("Circuit breaker",   "internal/domain/circuitbreaker — tracks provider failure state"),
        ("Intent parser",     "internal/domain/intent — maps X-IXR-Intent + complexity heuristic"),
        ("Scoring engine",    "internal/domain/scoring — bandit + regret-based model scorer"),
        ("Rate limiting",     "internal/domain/policy — per-tenant token-bucket enforcer"),
        ("Routing filter",    "internal/domain/routing — filters on cost/latency/circuit state"),
        ("Fallback chains",   "internal/domain/routing — BuildFallbackChain from scorer output"),
    ]
    for name, desc in rows:
        print(f"  {green('▸')}  {bold(name):24}  {dim(desc)}")
    print()
    note("phase-2_2 adds: ExecuteWithFallback, tenant.MemoryStore, config hot-reload,")
    note("secrets injection, and Redis/Postgres store interfaces for circuit/perf/policy data.")


# ── Feature summary table ─────────────────────────────────────────────────────

ALL_FEATURES = [
    ("basic_routing",           "Basic OpenAI-compatible proxy routing"),
    ("auto_routing",            "Auto-routing via task / budget / latency hints"),
    ("event_bus",               "In-process event bus + audit-log plugin"),
    ("circuit_breaker_domain",  "Circuit breaker domain package"),
    ("intent_parser",           "Intent parser (X-IXR-Intent / complexity)"),
    ("scoring_engine",          "Bandit scoring engine with regret tracking"),
    ("rate_limit_domain",       "Rate limiting domain (token bucket)"),
    ("routing_filter_scorer",   "Routing filter + scorer + fallback chain"),
    ("shadow_routing",          "Shadow routing (HTTP-wired, async, bus-published)"),
    ("streaming_sse",           "SSE StreamWriter + streamHandler skeleton"),
    ("telemetry_plugin",        "Telemetry plugin (CallEvent → TelemetryRecord)"),
    ("config_hot_reload",       "Config hot-reload + secret injection"),
    ("secrets_management",      "Secrets management in config layer"),
    ("tenant_management",       "Multi-tenant policy + credential selection"),
    ("executor_retry_fallback", "ExecuteWithFallback with retry + backoff"),
    ("redis_postgres_stores",   "Redis/Postgres store interfaces (circuit, perf, policy)"),
]

def print_feature_summary(features):
    print()
    print(f"  {bold(cyan('Branch feature summary'))}")
    print(f"  {dim('─' * 56)}")
    for key, label in ALL_FEATURES:
        mark = green("✓") if key in features else dim("·")
        print(f"    {mark}  {label}")
    print()
    print(f"  {dim('Run  ./demo/demo.sh <branch>  to demo a different branch.')}")
    print()

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port",   type=int, default=7001)
    parser.add_argument("--branch", default="phase-2_2")
    args = parser.parse_args()

    features  = features_for(args.branch)
    providers = detect_providers()

    print()
    print(f"  {bold('Branch:')}  {cyan(args.branch)}")
    print(f"  {bold('Server:')}  http://localhost:{args.port}")
    print()

    if not providers:
        print(f"  {red('No API keys detected.')} Set at least one of:")
        for env_var, _, label, free in FREE_PROVIDERS:
            tier = "free" if free else "paid"
            print(f"    export {env_var}=...   # {label}  [{tier}]")
        print()
        print(f"  {yellow('Tip:')} Free-tier keys available at:")
        print(f"  cerebras.ai  ·  console.groq.com  ·  console.mistral.ai  ·  cloud.sambanova.ai")
        sys.exit(1)

    print(f"  {bold('Configured providers:')}")
    for env_var, model, label, free in providers:
        tier = green("free") if free else yellow("paid")
        print(f"    {green('✓')}  {label}  [{tier}]")

    primary_env, primary_model, primary_label, _ = providers[0]
    secondary = providers[1] if len(providers) > 1 else None

    # Scenarios always available
    scenario_basic(args.port, primary_model, primary_label)
    scenario_auto_routing(args.port)
    scenario_tight_budget(args.port)
    scenario_latency(args.port)
    scenario_use_case(args.port, primary_model)

    # Shadow routing — wired in phase-2_2
    if "shadow_routing" in features:
        if secondary:
            _, shadow_model, shadow_label, _ = secondary
            scenario_shadow(args.port, primary_model, shadow_model, shadow_label)
        else:
            section("Scenario 6 — Shadow routing  (phase-2_2)")
            note("Shadow routing is wired in (X-IXR-Shadow-Model header).")
            note(f"Only one provider is configured ({primary_label}); add a second key to see it.")
    else:
        section("Scenario 6 — Shadow routing")
        note(f"Shadow routing was added in phase-2_2. Current branch: {bold(args.branch)}.")
        note("Switch to phase-2_2 to demo this feature.")

    if "streaming_sse" in features:
        scenario_streaming_note()

    if "circuit_breaker_domain" in features:
        scenario_domain_overview()

    print_feature_summary(features)


if __name__ == "__main__":
    main()
