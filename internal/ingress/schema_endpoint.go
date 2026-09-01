package ingress

import (
	"encoding/json"
	"net/http"
)

// SchemaHandler serves GET /v1/schema — a human- and machine-readable description
// of every type ixr accepts or returns. REST clients use the JSON Schema;
// non-Go consumers wanting typed bindings compile from schema/ixr.proto (kept
// in sync with the $defs below — see internal/ingress/schema_test.go).
type SchemaHandler struct{}

func NewSchemaHandler() *SchemaHandler { return &SchemaHandler{} }

func (h *SchemaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ixrJSONSchema)
}

// ixrJSONSchema is the canonical JSON Schema for ixr's public API surface.
// Generated from pkg/schema/* — update this when types change, and update
// schema/ixr.schema.json and schema/ixr.proto to match (schema_test.go fails
// CI if the $defs below drift from schema/ixr.schema.json's $defs).
var ixrJSONSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id":     "https://ixr.ix-labs.ai/v1/schema",
	"title":   "ixr API Schema",
	"version": "2",
	"$defs":   ixrJSONSchemaDefs,
	"paths": map[string]any{
		"/v1/chat/completions": map[string]any{
			"post": map[string]any{
				"requestBody":  map[string]any{"$ref": "#/$defs/RequestEnvelope"},
				"responseBody": map[string]any{"$ref": "#/$defs/ResponseEnvelope"},
			},
		},
		"/v1/embeddings": map[string]any{
			"post": map[string]any{
				"requestBody":  map[string]any{"$ref": "#/$defs/EmbeddingRequest"},
				"responseBody": map[string]any{"$ref": "#/$defs/EmbeddingResponse"},
			},
		},
		"/v1/images/generations": map[string]any{
			"post": map[string]any{
				"requestBody":  map[string]any{"$ref": "#/$defs/ImageRequest"},
				"responseBody": map[string]any{"$ref": "#/$defs/ImageResponse"},
			},
		},
		"/v1/audio/speech": map[string]any{
			"post": map[string]any{
				"requestBody": map[string]any{"$ref": "#/$defs/AudioSpeechRequest"},
			},
		},
		"/v1/audio/transcriptions": map[string]any{
			"post": map[string]any{
				"requestBody":  map[string]any{"$ref": "#/$defs/TranscriptionRequest"},
				"responseBody": map[string]any{"$ref": "#/$defs/TranscriptionResponse"},
			},
		},
		"/v1/schema": map[string]any{
			"get": map[string]any{"description": "Returns this schema document."},
		},
		"/metrics": map[string]any{
			"get": map[string]any{"description": "Prometheus metrics endpoint."},
		},
	},
}

// ixrJSONSchemaDefs mirrors schema/ixr.schema.json's "$defs" object exactly —
// kept as its own variable (rather than inlined into ixrJSONSchema above) so
// schema_test.go can compare it directly against the checked-in file without
// also comparing the live-endpoint-only "paths" section.
var ixrJSONSchemaDefs = map[string]any{
	"ContentPart": map[string]any{
		"description": "One block of a multi-part message (text + images).",
		"type":        "object",
		"required":    []string{"type"},
		"properties": map[string]any{
			"type":      map[string]any{"type": "string", "enum": []string{"text", "image_url"}},
			"text":      map[string]any{"type": "string"},
			"image_url": map[string]any{"$ref": "#/$defs/ImageURLPart"},
		},
	},
	"ImageURLPart": map[string]any{
		"description": "An image reference: an https:// URL, or a data: URI carrying inline base64 image bytes.",
		"type":        "object",
		"required":    []string{"url"},
		"properties": map[string]any{
			"url":    map[string]any{"type": "string"},
			"detail": map[string]any{"type": "string", "enum": []string{"auto", "low", "high"}},
		},
	},
	"Message": map[string]any{
		"description": "A single turn in a conversation.",
		"type":        "object",
		"required":    []string{"role"},
		"properties": map[string]any{
			"role": map[string]any{"type": "string", "enum": []string{"system", "user", "assistant", "tool"}},
			"content": map[string]any{
				"description": "Plain text, or an array of content parts for multimodal messages.",
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/ContentPart"}},
				},
			},
			"tool_calls":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/ToolCall"}},
			"tool_call_id": map[string]any{"type": "string", "description": "Set on role=\"tool\" messages; identifies which ToolCall this responds to."},
			"name":         map[string]any{"type": "string", "description": "Optional function name on role=\"tool\" messages."},
		},
	},
	"ToolCall": map[string]any{
		"description": "A single tool invocation requested by the model.",
		"type":        "object",
		"required":    []string{"id", "type", "function"},
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"type":     map[string]any{"type": "string", "enum": []string{"function"}},
			"function": map[string]any{"$ref": "#/$defs/ToolFunction"},
		},
	},
	"ToolFunction": map[string]any{
		"type":     "object",
		"required": []string{"name", "arguments"},
		"properties": map[string]any{
			"name":      map[string]any{"type": "string"},
			"arguments": map[string]any{"type": "string", "description": "JSON-encoded string of the function arguments."},
		},
	},
	"Tool": map[string]any{
		"description": "A callable function the model may invoke.",
		"type":        "object",
		"required":    []string{"type", "function"},
		"properties": map[string]any{
			"type":     map[string]any{"type": "string", "enum": []string{"function"}},
			"function": map[string]any{"$ref": "#/$defs/FunctionDef"},
		},
	},
	"FunctionDef": map[string]any{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"parameters":  map[string]any{"type": "object", "description": "JSON Schema object describing the function parameters."},
			"strict":      map[string]any{"type": "boolean"},
		},
	},
	"RequestEnvelope": map[string]any{
		"description": "ixr's canonical inbound chat request. Mirrors the OpenAI /v1/chat/completions request body.",
		"type":        "object",
		"required":    []string{"model", "messages"},
		"properties": map[string]any{
			"model":    map[string]any{"type": "string", "description": "Model name or 'auto'"},
			"messages": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Message"}, "minItems": 1},
			"stream":   map[string]any{"type": "boolean", "default": false},
			"tools":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Tool"}},
			"tool_choice": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string", "enum": []string{"none", "auto", "required"}},
					map[string]any{"$ref": "#/$defs/ToolChoiceObject"},
				},
			},
			"temperature": map[string]any{"type": "number", "minimum": 0, "maximum": 2},
			"max_tokens":  map[string]any{"type": "integer", "minimum": 1},
			"top_p":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"n":           map[string]any{"type": "integer", "minimum": 1},
			"stop": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4},
				},
			},
			"user": map[string]any{"type": "string"},
		},
	},
	"ToolChoiceObject": map[string]any{
		"type":     "object",
		"required": []string{"type", "function"},
		"properties": map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"function"}},
			"function": map[string]any{
				"type":       "object",
				"required":   []string{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
	},
	"Choice": map[string]any{
		"type":     "object",
		"required": []string{"index", "message", "finish_reason"},
		"properties": map[string]any{
			"index":         map[string]any{"type": "integer"},
			"message":       map[string]any{"$ref": "#/$defs/Message"},
			"finish_reason": map[string]any{"type": "string", "enum": []string{"stop", "length", "tool_calls", "content_filter", "null"}},
		},
	},
	"Usage": map[string]any{
		"type":     "object",
		"required": []string{"prompt_tokens", "completion_tokens", "total_tokens"},
		"properties": map[string]any{
			"prompt_tokens":               map[string]any{"type": "integer"},
			"completion_tokens":           map[string]any{"type": "integer"},
			"total_tokens":                map[string]any{"type": "integer"},
			"cache_read_input_tokens":     map[string]any{"type": "integer", "description": "Anthropic-style prompt-cache read tokens, included in prompt_tokens (not additional). 0 when unsupported or unused."},
			"cache_creation_input_tokens": map[string]any{"type": "integer", "description": "Anthropic-style prompt-cache write tokens, included in prompt_tokens (not additional). 0 when unsupported or unused."},
		},
	},
	"ResponseEnvelope": map[string]any{
		"description": "ixr's canonical chat response. Shaped to match the OpenAI response format.",
		"type":        "object",
		"required":    []string{"id", "object", "created", "model", "choices", "usage"},
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"object":  map[string]any{"type": "string", "enum": []string{"chat.completion"}},
			"created": map[string]any{"type": "integer", "description": "Unix timestamp (seconds)."},
			"model":   map[string]any{"type": "string"},
			"choices": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Choice"}},
			"usage":   map[string]any{"$ref": "#/$defs/Usage"},
		},
	},
	"EmbeddingRequest": map[string]any{
		"description": "Canonical form of a POST /v1/embeddings body.",
		"type":        "object",
		"required":    []string{"model", "input"},
		"properties": map[string]any{
			"model": map[string]any{"type": "string"},
			"input": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"encoding_format": map[string]any{"type": "string", "enum": []string{"float", "base64"}},
			"dimensions":      map[string]any{"type": "integer"},
			"user":            map[string]any{"type": "string"},
		},
	},
	"EmbeddingObject": map[string]any{
		"type":     "object",
		"required": []string{"object", "index", "embedding"},
		"properties": map[string]any{
			"object":    map[string]any{"type": "string", "enum": []string{"embedding"}},
			"index":     map[string]any{"type": "integer"},
			"embedding": map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
		},
	},
	"EmbeddingUsage": map[string]any{
		"type":     "object",
		"required": []string{"prompt_tokens", "total_tokens"},
		"properties": map[string]any{
			"prompt_tokens": map[string]any{"type": "integer"},
			"total_tokens":  map[string]any{"type": "integer"},
		},
	},
	"EmbeddingResponse": map[string]any{
		"description": "Canonical form of a /v1/embeddings response.",
		"type":        "object",
		"required":    []string{"object", "data", "model", "usage"},
		"properties": map[string]any{
			"object": map[string]any{"type": "string", "enum": []string{"list"}},
			"data":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/EmbeddingObject"}},
			"model":  map[string]any{"type": "string"},
			"usage":  map[string]any{"$ref": "#/$defs/EmbeddingUsage"},
		},
	},
	"ImageRequest": map[string]any{
		"description": "Canonical form of a POST /v1/images/generations body.",
		"type":        "object",
		"required":    []string{"model", "prompt"},
		"properties": map[string]any{
			"model":           map[string]any{"type": "string"},
			"prompt":          map[string]any{"type": "string"},
			"n":               map[string]any{"type": "integer", "description": "Number of images (default 1)."},
			"size":            map[string]any{"type": "string", "description": "e.g. \"1024x1024\"."},
			"quality":         map[string]any{"type": "string", "enum": []string{"standard", "hd"}},
			"style":           map[string]any{"type": "string", "enum": []string{"vivid", "natural"}},
			"response_format": map[string]any{"type": "string", "enum": []string{"url", "b64_json"}},
			"user":            map[string]any{"type": "string"},
		},
	},
	"ImageData": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":            map[string]any{"type": "string"},
			"b64_json":       map[string]any{"type": "string"},
			"revised_prompt": map[string]any{"type": "string"},
		},
	},
	"ImageResponse": map[string]any{
		"description": "Canonical form of a /v1/images/generations response.",
		"type":        "object",
		"required":    []string{"created", "data"},
		"properties": map[string]any{
			"created": map[string]any{"type": "integer"},
			"data":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/ImageData"}},
		},
	},
	"AudioSpeechRequest": map[string]any{
		"description": "Canonical form of a POST /v1/audio/speech body.",
		"type":        "object",
		"required":    []string{"model", "input", "voice"},
		"properties": map[string]any{
			"model":           map[string]any{"type": "string"},
			"input":           map[string]any{"type": "string"},
			"voice":           map[string]any{"type": "string", "enum": []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}},
			"response_format": map[string]any{"type": "string", "enum": []string{"mp3", "opus", "aac", "flac", "wav", "pcm"}},
			"speed":           map[string]any{"type": "number", "minimum": 0.25, "maximum": 4.0},
		},
	},
	"TranscriptionRequest": map[string]any{
		"description": "Canonical form of a POST /v1/audio/transcriptions body. The audio file itself is sent as multipart form-data (field \"file\"), so it has no representation here.",
		"type":        "object",
		"required":    []string{"model"},
		"properties": map[string]any{
			"model":           map[string]any{"type": "string"},
			"language":        map[string]any{"type": "string"},
			"prompt":          map[string]any{"type": "string"},
			"temperature":     map[string]any{"type": "number"},
			"response_format": map[string]any{"type": "string", "enum": []string{"json", "text", "srt", "verbose_json", "vtt"}},
		},
	},
	"TranscriptionResponse": map[string]any{
		"type":       "object",
		"required":   []string{"text"},
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	},
	"CostBreakdown": map[string]any{
		"description": "USD cost components for a single LLM call.",
		"type":        "object",
		"required":    []string{"input_usd", "output_usd", "total_usd"},
		"properties": map[string]any{
			"input_usd":  map[string]any{"type": "number"},
			"output_usd": map[string]any{"type": "number"},
			"total_usd":  map[string]any{"type": "number"},
		},
	},
	"Identity": map[string]any{
		"description": "Caller's tenant, team, user, and use-case context.",
		"type":        "object",
		"required":    []string{"tenant_id"},
		"properties": map[string]any{
			"tenant_id":   map[string]any{"type": "string"},
			"team_id":     map[string]any{"type": "string"},
			"user_id":     map[string]any{"type": "string"},
			"use_case_id": map[string]any{"type": "string"},
		},
	},
	"ShadowMetadata": map[string]any{
		"description": "Attached to CallEvents emitted for shadow-routed requests.",
		"type":        "object",
		"properties": map[string]any{
			"primary_id":    map[string]any{"type": "string"},
			"primary_model": map[string]any{"type": "string"},
			"shadow_model":  map[string]any{"type": "string"},
		},
	},
	"CallEvent": map[string]any{
		"description": "Emitted on the ixr event bus for every LLM call. Primary unit of data for plugins and downstream systems.",
		"type":        "object",
		"required":    []string{"id", "timestamp", "tenant_id", "provider", "model"},
		"properties": map[string]any{
			"id":            map[string]any{"type": "string"},
			"timestamp":     map[string]any{"type": "string", "format": "date-time"},
			"use_case_id":   map[string]any{"type": "string"},
			"tenant_id":     map[string]any{"type": "string"},
			"team_id":       map[string]any{"type": "string"},
			"user_id":       map[string]any{"type": "string"},
			"provider":      map[string]any{"type": "string", "description": "Normalised provider name (openai, anthropic, vertex_ai, mistral_ai, ollama, deepseek)."},
			"model":         map[string]any{"type": "string"},
			"latency_ms":    map[string]any{"type": "integer", "description": "Provider round-trip in milliseconds."},
			"tokens_in":     map[string]any{"type": "integer"},
			"tokens_out":    map[string]any{"type": "integer"},
			"cost":          map[string]any{"$ref": "#/$defs/CostBreakdown"},
			"request":       map[string]any{"$ref": "#/$defs/RequestEnvelope"},
			"response":      map[string]any{"$ref": "#/$defs/ResponseEnvelope"},
			"error":         map[string]any{"type": "string", "description": "Non-empty when the provider call failed."},
			"streaming":     map[string]any{"type": "boolean"},
			"shadow":        map[string]any{"$ref": "#/$defs/ShadowMetadata"},
			"auto_routed":   map[string]any{"type": "boolean", "description": "True when the call arrived via model:\"auto\"."},
			"fallback_used": map[string]any{"type": "boolean", "description": "True when a fallback model served the call after fallback_from failed."},
			"fallback_from": map[string]any{"type": "string"},
		},
	},
	"TelemetryRecord": map[string]any{
		"description": "Written by the telemetry plugin for every call. Carries routing metadata beyond CallEvent.",
		"type":        "object",
		"required":    []string{"request_id", "tenant_id", "model", "provider", "timestamp"},
		"properties": map[string]any{
			"request_id":     map[string]any{"type": "string"},
			"use_case_id":    map[string]any{"type": "string"},
			"tenant_id":      map[string]any{"type": "string"},
			"intent":         map[string]any{"type": "string"},
			"model":          map[string]any{"type": "string"},
			"response_model": map[string]any{"type": "string", "description": "Actual model returned by provider (may differ from request model)."},
			"provider":       map[string]any{"type": "string"},
			"latency_ms":     map[string]any{"type": "integer"},
			"tokens_in":      map[string]any{"type": "integer"},
			"tokens_out":     map[string]any{"type": "integer"},
			"max_tokens":     map[string]any{"type": "integer"},
			"cost_usd":       map[string]any{"type": "number"},
			"success":        map[string]any{"type": "boolean"},
			"error_message":  map[string]any{"type": "string"},
			"finish_reason":  map[string]any{"type": "string"},
			"fallback_used":  map[string]any{"type": "boolean"},
			"fallback_from":  map[string]any{"type": "string"},
			"shadow":         map[string]any{"type": "boolean"},
			"shadow_of":      map[string]any{"type": "string"},
			"timestamp":      map[string]any{"type": "string", "format": "date-time"},
		},
	},
}
