# ixr Schema — v1

Formal type definitions for the ixr event bus and API contracts. Use these if you are building a plugin or consuming the ixr event stream in a language other than Go.

**Go is the source of truth.** The types live in [`pkg/schema/`](../pkg/schema/). This directory contains derived artifacts — kept in sync manually with the Go types. If you find a discrepancy, the Go types win.

---

## Files

| File | Format | Use when |
|---|---|---|
| `ixr.proto` | Protocol Buffers v3 | You want generated, typed bindings in Python, TypeScript, Rust, Java, C#, etc. |
| `ixr.schema.json` | JSON Schema (draft 2020-12) | You want to validate JSON payloads or generate types without a proto toolchain |

---

## Key types

| Type | Description |
|---|---|
| `CallEvent` | **Primary bus event.** Emitted for every LLM call. This is what plugin `OnEvent` receives. |
| `RequestEnvelope` | Inbound chat request (OpenAI-compatible). |
| `ResponseEnvelope` | Outbound chat response (OpenAI-compatible). |
| `Message` | A single conversation turn (`role` + `content`). |
| `TelemetryRecord` | Extended record from the telemetry plugin — includes routing metadata, cost, fallback info. |
| `Identity` | Caller's tenant / team / user context, resolved from request headers. |
| `CostBreakdown` | USD cost components for a call. |

---

## Usage

### Python (from proto)

```bash
pip install grpcio-tools
python -m grpc_tools.protoc -I. --python_out=. schema/ixr.proto
```

```python
from schema.ixr_pb2 import CallEvent
# deserialise from the ixr audit-log JSON output
import json
data = json.loads(line)
ev = CallEvent()
ev.id = data["id"]
print(ev.model, ev.tokens_in, ev.cost.total_usd)
```

### TypeScript (from proto)

```bash
npm install -g ts-proto
protoc --plugin=./node_modules/.bin/protoc-gen-ts_proto \
       --ts_proto_out=./src \
       schema/ixr.proto
```

```typescript
import { CallEvent } from "./src/schema/ixr";
const ev = CallEvent.fromJSON(JSON.parse(line));
console.log(ev.model, ev.tokensIn, ev.cost?.totalUsd);
```

### Rust (from proto)

```toml
# Cargo.toml
[build-dependencies]
prost-build = "0.13"
```

```rust
// build.rs
fn main() {
    prost_build::compile_protos(&["schema/ixr.proto"], &["."]).unwrap();
}
```

### JSON Schema validation (any language)

```python
# Python — validate a CallEvent JSON blob
import jsonschema, json

with open("schema/ixr.schema.json") as f:
    schema = json.load(f)

resolver = jsonschema.RefResolver.from_schema(schema)
validator = jsonschema.Draft202012Validator(
    schema["$defs"]["CallEvent"],
    resolver=resolver,
)
validator.validate(call_event_dict)
```

---

## Reading the event stream

ixr's audit-log plugin writes one `CallEvent` as a JSON line to stdout per call. Pipe it to your consumer:

```bash
./ixr | python my_plugin.py
```

```python
# my_plugin.py
import sys, json

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        ev = json.loads(line)
    except json.JSONDecodeError:
        continue

    # ev is a CallEvent — see ixr.schema.json for the full shape
    print(f"{ev['model']} | {ev['tokens_in']}→{ev['tokens_out']} | ${ev['cost']['total_usd']:.4f}")
```

---

## Versioning

This schema follows the same semver contract as `pkg/`. Breaking changes (field removal, type change, rename) require a major version bump. New optional fields are non-breaking.

Current version: **v1**
