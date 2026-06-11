#!/usr/bin/env python3
"""
run_demo.py — ixr demo: shows what the router actually does, then lets you chat.

Usage (via demo.sh):
    python3 run_demo.py --port 7001 --branch phase-2_2 [--log /tmp/ixr-demo/ixr.log]
"""

import argparse
import json
import os
import sys
import time
import threading
import urllib.error
import urllib.request

# ── ANSI ──────────────────────────────────────────────────────────────────────

def _c(code, t): return f"\033[{code}m{t}\033[0m"
def bold(t):   return _c("1", t)
def cyan(t):   return _c("1;36", t)
def green(t):  return _c("1;32", t)
def yellow(t): return _c("1;33", t)
def red(t):    return _c("1;31", t)
def dim(t):    return _c("2", t)
def white(t):  return _c("1;37", t)

# ── Branch features ───────────────────────────────────────────────────────────

BRANCH_FEATURES = {
    "main":               {"basic_routing", "auto_routing", "event_bus"},
    "demo_test":          {"basic_routing", "auto_routing", "event_bus"},
    "demo_cross_compile": {"basic_routing", "auto_routing", "event_bus"},
    "demo-chain":         {"basic_routing", "auto_routing", "event_bus", "chain_routing"},
    "demo-chain-ui":      {"basic_routing", "auto_routing", "event_bus", "chain_routing"},
    "phase-2": {
        "basic_routing", "auto_routing", "event_bus",
        "circuit_breaker_domain", "intent_parser", "scoring_engine",
        "rate_limit_domain", "routing_filter_scorer",
    },
    "phase-2_2": {
        "basic_routing", "auto_routing", "event_bus",
        "circuit_breaker_domain", "intent_parser", "scoring_engine",
        "rate_limit_domain", "routing_filter_scorer",
        "shadow_routing", "streaming_sse", "telemetry_plugin",
        "config_hot_reload", "secrets_management", "tenant_management",
        "executor_retry_fallback", "redis_postgres_stores",
    },
    "phase-2_3": {
        "basic_routing", "auto_routing", "event_bus",
        "circuit_breaker_domain", "intent_parser", "scoring_engine",
        "rate_limit_domain", "routing_filter_scorer",
        "shadow_routing", "streaming_sse", "telemetry_plugin",
        "config_hot_reload", "secrets_management", "tenant_management",
        "executor_retry_fallback", "redis_postgres_stores",
        "observability_otel", "prometheus_metrics", "request_id_propagation",
        "semantic_cache", "bus_adapters_fanout", "schema_registry",
        "bedrock_provider", "ollama_llamacpp_providers",
        "embeddings_endpoint", "images_endpoint", "full_tool_calling",
    },
}

def features_for(branch):
    b = branch.removeprefix("origin/").removeprefix("remotes/origin/")
    return BRANCH_FEATURES.get(b, BRANCH_FEATURES.get("main", set()))

# ── Provider detection ────────────────────────────────────────────────────────

FREE_PROVIDERS = [
    ("CEREBRAS_API_KEY",  "gpt-oss-120b",               "Cerebras",   "gpt-oss-120b",       True),
    ("CEREBRAS_API_KEY",  "zai-glm-4.7",                "Cerebras",   "zai-glm-4.7",        True),
    ("GROQ_API_KEY",      "llama-3.1-8b-instant",       "Groq/Llama", "llama-3.1-8b-instant", True),
    ("MISTRAL_API_KEY",   "mistral-small-latest",        "Mistral",    "mistral-small",      True),
    ("SAMBANOVA_API_KEY", "Meta-Llama-3.1-8B-Instruct", "SambaNova",  "Meta-Llama-3.1-8B", True),
    ("GITHUB_TOKEN",      "openai/gpt-4.1-mini",        "GitHub",     "gpt-4.1-mini",       True),
]
PAID_PROVIDERS = [
    ("OPENAI_API_KEY",    "gpt-4o-mini",       "OpenAI",    "gpt-4o-mini",         False),
    ("ANTHROPIC_API_KEY", "claude-sonnet-4-6", "Anthropic", "claude-sonnet-4-6",   False),
    ("GOOGLE_API_KEY",    "gemini-1.5-flash",  "Gemini",    "gemini-1.5-flash",    False),
]

def detect_providers():
    out = []
    for entry in FREE_PROVIDERS + PAID_PROVIDERS:
        if os.environ.get(entry[0], "").strip():
            out.append(entry)
    return out  # (env_var, model, provider_name, model_short, free)

# ── HTTP ──────────────────────────────────────────────────────────────────────

def chat(port, payload, extra_headers=None, timeout=45):
    """POST /v1/chat/completions. Returns (resp_dict, latency_sec, error_str)."""
    url = f"http://localhost:{port}/v1/chat/completions"
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    for k, v in (extra_headers or {}).items():
        req.add_header(k, v)
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            lat = time.monotonic() - t0
            return json.loads(r.read()), lat, None
    except urllib.error.HTTPError as e:
        lat = time.monotonic() - t0
        try:
            msg = json.loads(e.read()).get("error", {}).get("message", str(e))
        except Exception:
            msg = str(e)
        return None, lat, f"HTTP {e.code}: {msg}"
    except Exception as e:
        return None, time.monotonic() - t0, str(e)

def chat_parallel(port, payloads_headers):
    """Fire multiple chat calls in parallel. Returns list of (resp, lat, err)."""
    results = [None] * len(payloads_headers)
    def call(i, payload, headers):
        results[i] = chat(port, payload, headers)
    threads = []
    for i, (payload, headers) in enumerate(payloads_headers):
        t = threading.Thread(target=call, args=(i, payload, headers))
        t.start()
        threads.append(t)
    for t in threads:
        t.join()
    return results

# ── Audit log reader ──────────────────────────────────────────────────────────

LOG_FILE = ""  # set by main() from --log argument

def _log_pos():
    """Return the current byte size of LOG_FILE (used as a read-start marker)."""
    if not LOG_FILE:
        return 0
    try:
        return os.path.getsize(LOG_FILE)
    except OSError:
        return 0

def _parse_call_events(text):
    """Extract CallEvent dicts from a chunk of log text (skips non-JSON lines)."""
    events = []
    for line in text.splitlines():
        line = line.strip()
        if not line or line[0] != "{":
            continue
        try:
            ev = json.loads(line)
            if isinstance(ev, dict) and "provider" in ev and "tokens_in" in ev:
                events.append(ev)
        except json.JSONDecodeError:
            pass
    return events

def read_event_after(pos, timeout=2.0):
    """Return the first CallEvent written to LOG_FILE after byte offset pos."""
    if not LOG_FILE:
        return None
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if os.path.getsize(LOG_FILE) > pos:
                with open(LOG_FILE) as f:
                    f.seek(pos)
                    events = _parse_call_events(f.read())
                if events:
                    return events[0]
        except OSError:
            pass
        time.sleep(0.05)
    return None

def read_events_after(pos, count, timeout=3.0):
    """Return up to count CallEvents written to LOG_FILE after byte offset pos."""
    if not LOG_FILE:
        return []
    deadline = time.monotonic() + timeout
    seen_ids = set()
    events = []
    while time.monotonic() < deadline and len(events) < count:
        try:
            if os.path.getsize(LOG_FILE) > pos:
                with open(LOG_FILE) as f:
                    f.seek(pos)
                    for ev in _parse_call_events(f.read()):
                        eid = ev.get("id", id(ev))
                        if eid not in seen_ids:
                            seen_ids.add(eid)
                            events.append(ev)
        except OSError:
            pass
        if len(events) < count:
            time.sleep(0.1)
    return events

# ── Display ───────────────────────────────────────────────────────────────────

def hr(char="─", width=60):
    print(f"  {dim(char * width)}")

def section(title):
    print()
    hr("─")
    print(f"  {bold(cyan(title))}")
    hr("─")

def note(msg):
    print(f"  {yellow('i')}  {dim(msg)}")

def response_box(label, resp, lat, err, width=56):
    top    = f"  +-- {bold(label)} " + "-" * max(0, width - len(label) - 5) + "+"
    bottom = "  +" + "-" * (width + 1) + "+"
    print(top)
    if err:
        print(f"  |  {red('Error:')} {err[:width-10]}")
    elif resp:
        choices = resp.get("choices", [])
        content = choices[0]["message"]["content"] if choices else ""
        model   = resp.get("model", "?")
        usage   = resp.get("usage", {})
        t_in    = usage.get("prompt_tokens", "?")
        t_out   = usage.get("completion_tokens", "?")
        # Wrap content
        words, line = content.split(), ""
        for word in words:
            if len(line) + len(word) + 1 > width - 4:
                print(f"  |  {dim(line)}")
                line = word
            else:
                line = (line + " " + word).strip()
        if line:
            print(f"  |  {dim(line)}")
        print(f"  |  {dim('')}")
        print(f"  |  {green(model)}  {dim(f'{lat*1000:.0f}ms | in:{t_in} out:{t_out}')}")
    print(bottom)

def show_event(ev, client_lat_ms=None):
    """
    Print every field of an ixr CallEvent (from the audit-log plugin).

    Fields displayed:
      id, timestamp, use_case_id, provider, model,
      latency_ms (server, raw nanoseconds + converted ms),
      latency client-side (if provided),
      tokens_in, tokens_out,
      cost { input_usd, output_usd, total_usd },
      request { model, messages[] },
      response { id, object, created, model, usage{}, choices[] },
      error (if present)
    """
    if ev is None:
        return

    KW = 26  # visible key-column width (plain chars, before dim() wrapping)

    def kv(key, val):
        # ljust on the plain string so ANSI codes in val don't affect alignment
        padded = (key + ":").ljust(KW)
        print(f"  {dim(padded)}  {val}")

    def wrap_val(key, text, wrap=60):
        """Print text wrapped at wrap chars; continuation lines are indented."""
        # Collapse newlines and extra whitespace so they don't corrupt the layout.
        text = " ".join(text.split())
        parts = [text[i:i+wrap] for i in range(0, max(1, len(text)), wrap)]
        kv(key, dim(parts[0]))
        indent = " " * (KW + 2)  # 2 + KW + 2 = same column as value on first line
        for part in parts[1:]:
            print(f"  {indent}{dim(part)}")

    print()
    hr("·")
    print(f"  {bold(white('ixr event'))}  {dim('audit-log · CallEvent')}")
    hr("·")

    kv("id",          str(ev.get("id", "?")))
    kv("timestamp",   str(ev.get("timestamp", "?")))
    kv("use_case_id", str(ev.get("use_case_id") or "(none)"))
    kv("provider",    green(str(ev.get("provider", "?"))))
    kv("model",       bold(str(ev.get("model", "?"))))

    lat_raw = ev.get("latency_ms")
    if isinstance(lat_raw, (int, float)) and lat_raw > 0:
        kv("latency_ms (server)", f"{lat_raw} ms")
    else:
        kv("latency_ms (server)", str(lat_raw))
    if client_lat_ms is not None:
        kv("latency (client)",   f"{client_lat_ms:.0f} ms")

    kv("tokens_in",  str(ev.get("tokens_in", "?")))
    kv("tokens_out", str(ev.get("tokens_out", "?")))

    cost = ev.get("cost", {})
    kv("cost.input_usd",  f"{float(cost.get('input_usd',  0)):.6f}")
    kv("cost.output_usd", f"{float(cost.get('output_usd', 0)):.6f}")
    kv("cost.total_usd",  f"{float(cost.get('total_usd',  0)):.6f}")

    req = ev.get("request", {})
    kv("request.model", str(req.get("model", "?")))
    for i, msg in enumerate(req.get("messages", [])):
        role    = msg.get("role", "?")
        content = str(msg.get("content") or "")
        wrap_val(f"request.messages[{i}]", f"[{role}] {content}")

    resp = ev.get("response", {})
    kv("response.id",      str(resp.get("id", "?")))
    kv("response.object",  str(resp.get("object", "?")))
    kv("response.created", str(resp.get("created", "?")))
    kv("response.model",   str(resp.get("model", "?")))

    usage = resp.get("usage", {})
    kv("usage.prompt_tokens",     str(usage.get("prompt_tokens", "?")))
    kv("usage.completion_tokens", str(usage.get("completion_tokens", "?")))
    kv("usage.total_tokens",      str(usage.get("total_tokens", "?")))

    for choice in (resp.get("choices") or []):
        idx     = choice.get("index", 0)
        msg_c   = choice.get("message", {})
        content = str(msg_c.get("content") or "")
        kv(f"choices[{idx}].index",         str(idx))
        kv(f"choices[{idx}].role",          str(msg_c.get("role", "?")))
        kv(f"choices[{idx}].finish_reason", str(choice.get("finish_reason", "?")))
        wrap_val(f"choices[{idx}].content", content)

    if ev.get("error"):
        kv("error", red(str(ev["error"])))

    chain_meta = ev.get("chain")
    if chain_meta:
        kv("chain.id",    str(chain_meta.get("id", "?")))
        if chain_meta.get("name"):
            kv("chain.name",  str(chain_meta["name"]))
        kv("chain.stage", f"{chain_meta.get('stage', '?')} / {chain_meta.get('total_stages', '?')}")

    hr("·")

# ── Routing decision explainer ────────────────────────────────────────────────

# Mirrors the catalog in internal/domain/routing/router.go
# Full catalog: (model, cost_in/1M, cost_out/1M, latency_sec, failure_rate,
#                reasoning, coding, math, multilingual)
CATALOG = [
    ("claude-sonnet-4-6",        3.00, 15.00, 1.2, 0.020, 0.95, 0.92, 0.95, 0.90),
    ("gpt-4o-mini",              0.15,  0.60, 0.5, 0.025, 0.85, 0.88, 0.87, 0.82),
    ("gemini-1.5-flash",        0.075,  0.30, 0.6, 0.025, 0.84, 0.82, 0.86, 0.92),
    ("deepseek-chat",            0.27,  1.10, 2.0, 0.035, 0.83, 0.85, 0.88, 0.76),
    ("llama-3.3-70b-versatile",  0.0,   0.0,  0.8, 0.040, 0.80, 0.76, 0.80, 0.76),
    ("gpt-oss-120b",             0.0,   0.0,  0.5, 0.040, 0.74, 0.72, 0.75, 0.72),
    ("zai-glm-4.7",              0.0,   0.0,  0.4, 0.045, 0.68, 0.65, 0.68, 0.80),
]

# Task → (reasoning, coding, math, multilingual) weights sent as X-IXR-Task header.
# "general" sends no header — Go returns capability=0.75 for all models,
# so cost+latency determine the winner (free/fast models win).
TASK_WEIGHTS = {
    "reasoning":    (1, 0, 0, 0),
    "coding":       (0, 1, 0, 0),
    "math":         (0, 0, 1, 0),
    "multilingual": (0, 0, 0, 1),
    "general":      (0, 0, 0, 0),  # no task hint → server uses 0.75 fallback
}

_W_CAP, _W_COST, _W_LAT, _W_FAIL = 1.0, 0.18, 0.12, 0.10

def _normalize(v, lo, hi):
    if abs(hi - lo) < 1e-9:
        return 0.0
    return max(0.0, min(1.0, (v - lo) / (hi - lo)))

def explain_routing(task, budget, prompt_chars=0):
    """Return sorted list of (model, utility, cost_in, passes_budget) using the
    same formula as the Go routing engine."""
    weights = TASK_WEIGHTS.get(task, TASK_WEIGHTS["general"])
    wsum = sum(weights)

    # Estimate input share (mirrors Go estimateInputShare).
    if prompt_chars > 0:
        x = prompt_chars / 8000.0
        input_share = min(1.0, max(0.0, 0.25 + 0.5 * x / (x + 1)))
    else:
        input_share = 0.45

    costs = [
        input_share * c_in + (1 - input_share) * c_out
        for _, c_in, c_out, *_ in CATALOG
    ]
    latencies = [row[3] for row in CATALOG]
    min_cost, max_cost = min(costs), max(costs)
    min_lat,  max_lat  = min(latencies), max(latencies)

    rows = []
    for (model, c_in, c_out, lat, fail, *caps), blended in zip(CATALOG, costs):
        passes = (budget <= 0 or c_in <= budget)

        if wsum < 1e-9:
            capability = 0.75  # no task hint: Go returns constant 0.75
        else:
            capability = sum(w * c for w, c in zip(weights, caps)) / wsum

        norm_cost = _normalize(blended,  min_cost, max_cost)
        norm_lat  = _normalize(lat, min_lat,  max_lat)
        utility   = _W_CAP * capability - _W_COST * norm_cost - _W_LAT * norm_lat - _W_FAIL * fail

        rows.append((model, utility, c_in, passes))

    rows.sort(key=lambda r: (-r[3], -r[1]))  # budget-passing first, then utility
    return rows

# ── Scenarios ─────────────────────────────────────────────────────────────────

def scenario_routing_transparency(port, providers):
    section("Showcase 1 — What ixr actually does (routing transparency)")
    note("ixr sits between your code and the LLM. Same OpenAI-shaped request,")
    note("different provider under the hood. Here we send the exact same prompt")
    note("to every configured provider and show the results side by side.")
    print()

    question = "In one sentence, what is a neural network?"
    calls = [
        ({"model": m, "messages": [{"role": "user", "content": question}]}, {})
        for _, m, pname, _, _ in providers
    ]
    print(f"  Prompt: {bold(repr(question))}")
    print(f"  Firing {len(calls)} provider(s) in parallel via ixr...\n")

    pos = _log_pos()
    results = chat_parallel(port, calls)

    # Collect all events then match to results by response ID.
    raw_events = read_events_after(pos, len(results))
    events_by_id = {ev.get("id", ""): ev for ev in raw_events}

    for (_, m, pname, mshort, free), (resp, lat, err) in zip(providers, results):
        tier = green("free") if free else yellow("paid")
        label = f"{pname}/{mshort} [{tier}]"
        response_box(label, resp, lat, err)
        ev = events_by_id.get((resp or {}).get("id", "")) if resp else None
        show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)

    print()
    note("Your application sent ONE type of request. ixr handled all provider")
    note("differences (auth, base URL, API shape) transparently.")


def scenario_auto_routing(port):
    section("Showcase 2 — Auto-routing: ixr picks the model for you")
    note("Set model='auto' and pass task/budget headers. ixr's scoring engine")
    note("runs the catalog through a weighted capability + cost formula and picks")
    note("the best match. Here's the scoring table for each task type:\n")

    tasks = [
        ("coding",    2.0,  "X-IXR-Task: coding  X-IXR-Budget: 2.0"),
        ("reasoning", 0.0,  "X-IXR-Task: reasoning  (no budget cap)"),
        ("math",      0.30, "X-IXR-Task: math  X-IXR-Budget: 0.30"),
    ]

    for task, budget, hdrs in tasks:
        rows = explain_routing(task, budget)
        winner = next((r for r in rows if r[3]), None)
        print(f"  {bold(task.upper())}  {dim(hdrs)}")
        for model, utility, cost_in, passes in rows[:4]:
            marker = green("-> SELECTED") if (winner and model == winner[0]) else dim("         ")
            cost_s = f"${cost_in:.3f}/1M" if cost_in > 0 else "free"
            budget_s = "" if passes else red(" [over budget]")
            print(f"     {marker}  {model:<26} utility={utility:.3f}  {cost_s}{budget_s}")
        if not winner:
            print(f"     {red('No model passed the budget filter.')}")
        print()

        if winner:
            pos = _log_pos()
            resp, lat, err = chat(port, {
                "model": "auto",
                "messages": [{"role": "user", "content": f"What is {task}? One sentence."}],
            }, extra_headers={
                "X-IXR-Task": task,
                **({"X-IXR-Budget": str(budget)} if budget > 0 else {}),
            })
            if err and "no provider" in err:
                note(f"Catalog picked '{winner[0]}' but that provider key isn't configured.")
            elif err:
                note(f"Call failed: {err}")
            else:
                model_used = resp.get("model", "?") if resp else "?"
                content = resp["choices"][0]["message"]["content"] if resp and resp.get("choices") else ""
                print(f"     {green('Response via')} {bold(model_used)}  {dim(f'({lat*1000:.0f}ms)')}")
                print(f"     {dim(content[:120])}")
                ev = read_event_after(pos)
                show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)
            print()


def scenario_shadow(port, primary, shadow):
    section("Showcase 3 — Shadow routing (phase-2_2)")
    note("X-IXR-Shadow-Model fires a second call in the background.")
    note("The CALLER only waits for the primary. The shadow result goes to the")
    note("event bus so you can compare quality offline before switching providers.\n")

    _, pm, ppname, pmshort, _ = primary
    _, sm, spname, smshort, _ = shadow
    question = "What is the difference between a list and a tuple in Python?"

    print(f"  Prompt:  {bold(repr(question))}")
    print(f"  Primary: {bold(ppname + '/' + pmshort)}")
    print(f"  Shadow:  {bold(spname + '/' + smshort)} (async, not blocking caller)\n")

    pos = _log_pos()
    resp, lat, err = chat(port, {
        "model": pm,
        "messages": [{"role": "user", "content": question}],
    }, extra_headers={"X-IXR-Shadow-Model": sm})

    response_box(f"PRIMARY  {ppname}/{pmshort}", resp, lat, err)
    ev = read_event_after(pos)
    show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)

    print()
    print(f"  {yellow('Shadow call fired in background.')} ixr published:")
    print(f"  {dim('  CallEvent{shadow: {primary_id: resp.id, primary_model: ' + repr(pm) + ', shadow_model: ' + repr(sm) + '}}')}")
    note("Shadow response is in the bus — poll it to compare against primary.")


def scenario_semantic_cache(port, provider_entry):
    section("Showcase 4 — Semantic cache (phase-2_3)")
    note("ixr caches non-streaming responses by SHA-256(model + messages).")
    note("The second identical request is served from memory with X-Cache: HIT.\n")

    _, model, pname, mshort, _ = provider_entry
    question = "What does HTTP stand for?"
    payload = {"model": model, "messages": [{"role": "user", "content": question}]}

    print(f"  Prompt: {bold(repr(question))}\n")

    # First call — cache miss.
    pos1 = _log_pos()
    resp1, lat1, err1 = chat(port, payload)
    print(f"  {bold('Call 1')}  {dim('MISS (first call — cache cold)')}")
    response_box(f"{pname}/{mshort}", resp1, lat1, err1)
    ev1 = read_event_after(pos1)
    show_event(ev1, client_lat_ms=lat1 * 1000 if lat1 is not None else None)

    # Second call — should hit.
    pos2 = _log_pos()
    resp2, lat2, err2 = chat(port, payload)
    speedup = f"  {green(f'{lat1/lat2:.1f}x faster')}" if resp1 and resp2 and lat2 > 0 else ""
    print(f"\n  {bold('Call 2')}  {dim('HIT expected — served from in-memory cache')}{speedup}")
    response_box(f"{pname}/{mshort} [cached]", resp2, lat2, err2)
    ev2 = read_event_after(pos2)
    show_event(ev2, client_lat_ms=lat2 * 1000 if lat2 is not None else None)

    print()
    note("Cache is keyed on model + message content. Streaming requests bypass it.")
    note("Default: 1024-entry LRU, 5-minute TTL (override with IXR_CACHE_SIZE / IXR_CACHE_TTL_SEC).")


def scenario_schema_endpoint(port):
    section("Showcase 5 — Schema registry (phase-2_3)")
    note("GET /v1/schema returns the full JSON Schema for ixr's public API surface.")
    note("REST clients can validate requests against it; no proto compiler needed.\n")

    url = f"http://localhost:{port}/v1/schema"
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            schema = json.loads(r.read())
        paths = list(schema.get("paths", {}).keys())
        defs  = list(schema.get("$defs", {}).keys())
        print(f"  {green('GET /v1/schema')} -> {dim('200 OK')}")
        print(f"  {bold('Paths defined:')}  {dim(', '.join(paths))}")
        print(f"  {bold('Types defined:')}  {dim(', '.join(defs))}")
        print(f"  {bold('Schema version:')} {dim(str(schema.get('version', '?')))}")
    except Exception as e:
        print(f"  {red('Schema endpoint error:')} {e}")

    print()
    note("Compile api/proto/ixr.proto for gRPC clients — same types, binary wire format.")


def scenario_observability(port):
    section("Showcase 6 — Observability (phase-2_3)")
    note("Every request gets an X-Request-ID and an OTEL trace span.")
    note("Prometheus metrics are exposed on GET /metrics.\n")

    # Show a request ID being echoed back, and capture the full event.
    try:
        providers_local = detect_providers()
        if providers_local:
            _, model, pname, mshort, _ = providers_local[0]
            pos = _log_pos()
            t0 = time.monotonic()
            req = urllib.request.Request(
                f"http://localhost:{port}/v1/chat/completions",
                data=json.dumps({"model": model,
                                 "messages": [{"role": "user", "content": "ping"}]}).encode(),
                method="POST",
            )
            req.add_header("Content-Type", "application/json")
            with urllib.request.urlopen(req, timeout=15) as r:
                lat = time.monotonic() - t0
                req_id = r.headers.get("X-Request-ID", "(not present)")
            print(f"  {bold('X-Request-ID')} returned: {cyan(req_id)}")
            ev = read_event_after(pos)
            show_event(ev, client_lat_ms=lat * 1000)
    except Exception:
        pass

    # Show Prometheus metrics snippet.
    try:
        mreq = urllib.request.Request(f"http://localhost:{port}/metrics", method="GET")
        with urllib.request.urlopen(mreq, timeout=5) as r:
            lines = r.read().decode().splitlines()
        ixr_lines = [l for l in lines if l.startswith("ixr_")][:6]
        if ixr_lines:
            print(f"\n  {bold('GET /metrics')} (ixr_ family, first 6 lines):")
            for line in ixr_lines:
                print(f"    {dim(line)}")
        else:
            print(f"\n  {bold('GET /metrics')} -> {dim('200 OK')}  {dim('(scrape to see counters after traffic)')}")
    except Exception as e:
        print(f"  {red('/metrics error:')} {e}")

    print()
    note("Set IXR_OTLP_ENDPOINT=http://localhost:4318 to export traces to any OTLP collector.")

def scenario_chain(port, providers):
    section("Showcase — Multi-model chaining (X-IXR-Chain)")
    note("Add X-IXR-Chain to pipe a request through models in sequence.")
    note("Each stage sees the full conversation including every prior stage's reply.")
    note("Use a named chain from ixr.yaml or inline: 'model-a,model-b'\n")

    chain_name  = "fast-refine"
    stage_count = 2
    question    = "Explain what a hash table is and when to use one."

    print(f"  Prompt: {bold(repr(question))}")
    print(f"  Chain:  {bold(chain_name)}  {dim('(gpt-oss-120b drafts → zai-glm-4.7 refines)')}\n")

    pos = _log_pos()
    resp, lat, err = chat(port, {
        "model": chain_name,
        "messages": [{"role": "user", "content": question}],
    }, extra_headers={
        "X-IXR-Chain": chain_name,
        "X-IXR-UseCase": "demo-chain",
    }, timeout=90)

    if err:
        print(f"  {red('Chain error:')} {err}")
        note("Make sure CEREBRAS_API_KEY is set and fast-refine is in ixr.yaml.")
        return

    response_box(f"Chain result  ({chain_name})", resp, lat, err)

    events = read_events_after(pos, stage_count, timeout=max(30.0, (lat or 5) * 2 + 5))
    for ev in events:
        chain_meta = ev.get("chain", {})
        stage = chain_meta.get("stage", "?")
        total = chain_meta.get("total_stages", "?")
        model_name = ev.get("model", "?")
        print(f"\n  {bold(cyan(f'── Stage {stage} / {total}  ·  {model_name} ──'))}")
        show_event(ev)

    print()
    note("The final response is from the last stage. Each stage's event appears")
    note("in the audit log with chain.stage so you can trace the full pipeline.")


# ── Feature summary ───────────────────────────────────────────────────────────

ALL_FEATURES = [
    ("basic_routing",           "Basic OpenAI-compatible proxy routing"),
    ("auto_routing",            "Auto-routing via task / budget / latency hints"),
    ("event_bus",               "In-process event bus + audit-log plugin"),
    ("chain_routing",           "Multi-model chaining via X-IXR-Chain header"),
    ("circuit_breaker_domain",  "Circuit breaker domain package"),
    ("intent_parser",           "Intent parser (X-IXR-Intent / complexity)"),
    ("scoring_engine",          "Bandit scoring engine with regret tracking"),
    ("rate_limit_domain",       "Rate limiting domain (sliding window)"),
    ("routing_filter_scorer",   "Routing filter + scorer + fallback chain"),
    ("shadow_routing",          "Shadow routing (async, bus-published)"),
    ("streaming_sse",           "End-to-end SSE streaming for all providers"),
    ("telemetry_plugin",        "Telemetry plugin (CallEvent -> TelemetryRecord)"),
    ("config_hot_reload",       "Config hot-reload + secret injection"),
    ("secrets_management",      "Secrets management (Vault / AWS SSM)"),
    ("tenant_management",       "Multi-tenant policy + credential selection"),
    ("executor_retry_fallback", "Retry + exponential backoff + fallback chain"),
    ("redis_postgres_stores",   "Redis/Postgres store interfaces (stubs)"),
    ("observability_otel",      "OpenTelemetry traces (OTLP export, no-op default)"),
    ("prometheus_metrics",      "Prometheus metrics on GET /metrics"),
    ("request_id_propagation",  "X-Request-ID propagation on every request"),
    ("semantic_cache",          "Exact-match semantic cache (SHA-256, LRU+TTL)"),
    ("bus_adapters_fanout",     "Bus adapters: webhook fanout, NATS/Kafka/Kinesis/Pub/Sub stubs"),
    ("schema_registry",         "JSON Schema registry on GET /v1/schema + api/proto/ixr.proto"),
    ("bedrock_provider",        "AWS Bedrock provider (SigV4, no SDK)"),
    ("ollama_llamacpp_providers","Ollama + llama.cpp + generic local providers"),
    ("embeddings_endpoint",     "POST /v1/embeddings (provider.Embedder interface)"),
    ("images_endpoint",         "POST /v1/images/generations (provider.ImageGenerator)"),
    ("full_tool_calling",       "Full tool-calling spec (Tool, FunctionDef, ToolChoice)"),
]

def print_feature_summary(features, branch):
    section(f"What this branch adds  ({branch})")
    for key, label in ALL_FEATURES:
        mark = green("+ ") if key in features else dim("  ")
        print(f"    {mark}{label}")

# ── Interactive chat ──────────────────────────────────────────────────────────

MODES = {
    "1": "Direct (you choose the model)",
    "2": "Auto-route (ixr picks based on task)",
    "3": "Compare (same question, two models)",
    "4": "Chain (multi-model pipeline)",
}

def interactive_chat(port, providers, has_shadow):
    section("Interactive chat")
    note("ixr is still running. Try it yourself.\n")

    primary = providers[0]
    secondary = providers[1] if len(providers) > 1 else None
    _, pm, ppname, pmshort, _ = primary

    while True:
        print(f"  Mode:  {bold('[1]')} Direct  {bold('[2]')} Auto-route  {bold('[3]')} Compare  {bold('[4]')} Chain  {bold('[q]')} Quit")
        try:
            mode = input(f"  {cyan('Choose mode:')} ").strip().lower()
        except (EOFError, KeyboardInterrupt):
            print()
            break
        if mode in ("q", "quit", "exit"):
            break
        if mode not in ("1", "2", "3", "4"):
            continue

        try:
            question = input(f"  {cyan('Ask:')} ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            break
        if not question:
            continue

        print()

        if mode == "1":
            print(f"  {dim('Routing: direct -> ' + ppname + '/' + pmshort)}")
            pos = _log_pos()
            resp, lat, err = chat(port, {
                "model": pm,
                "messages": [{"role": "user", "content": question}],
            }, extra_headers={"X-IXR-UseCase": "demo-interactive"})
            response_box(f"{ppname}/{pmshort}", resp, lat, err)
            ev = read_event_after(pos)
            show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)

        elif mode == "2":
            tasks = ["general", "reasoning", "coding", "math", "multilingual"]
            print(f"  Task: {dim(', '.join(f'[{t[0]}]{t[1:]}' for t in tasks))} (enter=general)")
            try:
                task = input(f"  {cyan('Task type:')} ").strip().lower() or "general"
            except (EOFError, KeyboardInterrupt):
                print()
                break
            if task not in tasks:
                task = "general"

            rows = explain_routing(task, 0.0)
            winner = rows[0] if rows else None
            if winner:
                cost_s = f"${winner[2]:.3f}/1M" if winner[2] > 0 else "free"
                print(f"  {dim('ixr scoring -> selected:')} {bold(winner[0])}  "
                      f"{dim(f'(utility={winner[1]:.3f}, {cost_s})')}")
            extra = {"X-IXR-UseCase": "demo-interactive"}
            if task != "general":
                extra["X-IXR-Task"] = task
            pos = _log_pos()
            resp, lat, err = chat(port, {
                "model": "auto",
                "messages": [{"role": "user", "content": question}],
            }, extra_headers=extra)
            label = f"AUTO -> {resp.get('model', '?')}" if resp else "AUTO"
            response_box(label, resp, lat, err)
            if err and "no provider" in (err or ""):
                note(f"Catalog chose '{winner[0] if winner else '?'}' but that provider key isn't set.")
            ev = read_event_after(pos)
            show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)

        elif mode == "3":
            if len(providers) < 2:
                note("Only one provider is configured — can't compare. Add a second API key.")
            else:
                print()
                print(f"  {bold('Available models:')}")
                for i, (_, m, pname, mshort, free) in enumerate(providers):
                    tier = green("free") if free else yellow("paid")
                    print(f"    [{i+1}] {pname}/{mshort}  [{tier}]")
                print(f"    [a] All")
                try:
                    sel = input(f"  {cyan('Pick models to compare (e.g. 1,2 or a):')} ").strip().lower()
                except (EOFError, KeyboardInterrupt):
                    print()
                    break

                if sel in ("a", "all") or sel == "":
                    selected = providers
                else:
                    indices = []
                    for part in sel.split(","):
                        part = part.strip()
                        if part.isdigit():
                            idx = int(part) - 1
                            if 0 <= idx < len(providers):
                                indices.append(idx)
                    selected = [providers[i] for i in indices] if len(indices) >= 2 else providers[:2]

                print(f"\n  {dim(f'Firing {len(selected)} model(s) in parallel...')}")
                pos = _log_pos()
                results = chat_parallel(port, [
                    ({"model": m, "messages": [{"role": "user", "content": question}]}, {})
                    for _, m, _, _, _ in selected
                ])
                raw_events = read_events_after(pos, len(selected))
                events_by_id = {ev.get("id", ""): ev for ev in raw_events}

                for i, ((_, m, pname, mshort, free), (resp, lat, err)) in enumerate(zip(selected, results)):
                    tier = green("free") if free else yellow("paid")
                    print()
                    print(f"  {bold('═' * 58)}")
                    print(f"  {bold(f'  Model {i+1}/{len(selected)}  ·  {pname}/{mshort}')}  [{tier}]")
                    print(f"  {bold('═' * 58)}")
                    response_box(f"{pname}/{mshort}", resp, lat, err)
                    ev = events_by_id.get((resp or {}).get("id", "")) if resp else None
                    show_event(ev, client_lat_ms=lat * 1000 if lat is not None else None)

        elif mode == "4":
            NAMED_STAGES = {"fast-refine": 2, "smart-qa": 2, "debate": 3}
            print(f"  Named chains: {bold('fast-refine')}  {bold('smart-qa')}  {bold('debate')}")
            print(f"  Or inline:    {dim('gpt-oss-120b,zai-glm-4.7')}")
            try:
                chain_input = input(f"  {cyan('Chain:')} ").strip()
            except (EOFError, KeyboardInterrupt):
                print()
                break
            if not chain_input:
                continue

            stage_count = NAMED_STAGES.get(chain_input) or max(2, len([m for m in chain_input.split(",") if m.strip()]))

            print(f"  {dim(f'Running {stage_count}-stage chain...')}")
            pos = _log_pos()
            resp, lat, err = chat(port, {
                "model": chain_input,
                "messages": [{"role": "user", "content": question}],
            }, extra_headers={
                "X-IXR-Chain": chain_input,
                "X-IXR-UseCase": "demo-interactive",
            }, timeout=90)

            response_box(f"Chain: {chain_input}", resp, lat, err)

            events = read_events_after(pos, stage_count, timeout=max(30.0, stage_count * 15.0))
            for ev in events:
                chain_meta = ev.get("chain", {})
                stage      = chain_meta.get("stage", "?")
                total      = chain_meta.get("total_stages", "?")
                model_name = ev.get("model", "?")
                print(f"\n  {bold(cyan(f'── Stage {stage}/{total}  ·  {model_name} ──'))}")
                show_event(ev)

        print()

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    global LOG_FILE

    p = argparse.ArgumentParser()
    p.add_argument("--port",   type=int, default=8081)
    p.add_argument("--branch", default="main")
    p.add_argument("--log",    default="")
    args = p.parse_args()

    LOG_FILE = args.log

    features  = features_for(args.branch)
    providers = detect_providers()

    print()
    print(f"  {bold('ixr demo')}  |  branch: {cyan(args.branch)}  |  port: {args.port}")
    if LOG_FILE:
        print(f"  {dim('event log:')} {LOG_FILE}")
    print()

    if not providers:
        print(f"  {red('No API keys found.')} Set at least one:")
        for env_var, _, pname, _, free in FREE_PROVIDERS:
            print(f"    export {env_var}=...  # {pname}  [{'free' if free else 'paid'}]")
        sys.exit(1)

    print(f"  {bold('Providers:')}")
    for _, m, pname, mshort, free in providers:
        tier = green("free") if free else yellow("paid")
        print(f"    {green('v')}  {pname:<12} {mshort}  [{tier}]")

    # Showcases
    scenario_routing_transparency(args.port, providers)
    scenario_auto_routing(args.port)

    if "chain_routing" in features:
        scenario_chain(args.port, providers)

    if "shadow_routing" in features and len(providers) >= 2:
        scenario_shadow(args.port, providers[0], providers[1])
    elif "shadow_routing" in features:
        section("Showcase 3 — Shadow routing")
        note("Shadow routing is wired in. Add a second provider key to demo it.")

    if "semantic_cache" in features:
        scenario_semantic_cache(args.port, providers[0])

    if "schema_registry" in features:
        scenario_schema_endpoint(args.port)

    if "observability_otel" in features:
        scenario_observability(args.port)

    # What this branch has
    print_feature_summary(features, args.branch)

    # Interactive
    interactive_chat(args.port, providers, "shadow_routing" in features)

    print(f"\n  {green('Done.')}  Server log: {LOG_FILE or 'see demo.sh output'}\n")


if __name__ == "__main__":
    main()
