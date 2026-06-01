# ixr configuration (phase 2)

Full YAML spec with hot-reload boundaries and JSON schema for editor autocomplete.
This document is a placeholder — full spec ships in phase 2.

## phase 1 minimal config

```yaml
# ixr.yaml
port: 7000
providers:
  openai:
    api_key: ${OPENAI_API_KEY}
  anthropic:
    api_key: ${ANTHROPIC_API_KEY}
```

Environment variable interpolation via `${VAR}` syntax. No hot reload in phase 1.
