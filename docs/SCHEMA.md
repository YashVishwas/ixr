# ixr schema (v0)

> **Stability:** v0 — breaking changes are possible. Semver begins at v1.0.0.

All types live in `pkg/schema`. Everything there is the public API — third parties build on these types.

## CallEvent

Emitted on the bus after every LLM call.

```go
type CallEvent struct {
    ID        string           // unique call ID
    Timestamp time.Time
    UseCaseID string           // from X-IXR-UseCase header
    Provider  string           // "openai" | "anthropic" | ...
    Model     string           // e.g. "gpt-4o", "claude-3-5-sonnet-20241022"
    Latency   time.Duration    // total time including network
    TokensIn  int
    TokensOut int
    Cost      CostBreakdown
    Request   RequestEnvelope
    Response  ResponseEnvelope
    Error     string           // empty on success
    Shadow    *ShadowMetadata  // set only for shadow-routing events
}
```

`ShadowMetadata` links a shadow event back to the primary call:

```go
type ShadowMetadata struct {
    PrimaryID    string
    PrimaryModel string
    ShadowModel  string
}
```

## RequestEnvelope / ResponseEnvelope

ixr's canonical chat request and response, shaped to match the OpenAI format so existing SDKs work without changes.

## TelemetryRecord

Extended record written by the telemetry plugin. Adds routing metadata (intent,
fallback info, shadow markers) for the scoring engine.

## Versioning policy

- Minor versions: additive-only field additions to existing types
- Major versions: breaking changes, announced via RFC
- A proto definition for non-Go consumers will be published at `pkg/schema/schema.proto` (phase 2)
- Schema registry at `/schema/v1` (phase 2)
