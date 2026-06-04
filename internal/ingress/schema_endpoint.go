package ingress

import (
	"encoding/json"
	"net/http"
)

// SchemaHandler serves GET /v1/schema — a human- and machine-readable description
// of every type ixr accepts or returns. REST clients use the JSON Schema;
// gRPC clients compile from api/proto/ixr.proto.
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
// Generated from pkg/schema/* — update this when types change.
var ixrJSONSchema = map[string]any{
	"$schema":  "https://json-schema.org/draft/2020-12/schema",
	"$id":      "https://ixr.ix-labs.ai/v1/schema",
	"title":    "ixr API Schema",
	"version":  "2",
	"$defs": map[string]any{
		"Message": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"role":       map[string]any{"type": "string", "enum": []string{"system", "user", "assistant", "tool"}},
				"content":    map[string]any{"type": "string"},
				"tool_calls": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/ToolCall"}},
				"tool_call_id": map[string]any{"type": "string"},
			},
			"required": []string{"role"},
		},
		"Tool": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":     map[string]any{"type": "string", "const": "function"},
				"function": map[string]any{"$ref": "#/$defs/FunctionDef"},
			},
			"required": []string{"type", "function"},
		},
		"FunctionDef": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"parameters":  map[string]any{"type": "object"},
				"strict":      map[string]any{"type": "boolean"},
			},
			"required": []string{"name"},
		},
		"ToolCall": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":   map[string]any{"type": "string"},
				"type": map[string]any{"type": "string", "const": "function"},
				"function": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":      map[string]any{"type": "string"},
						"arguments": map[string]any{"type": "string"},
					},
				},
			},
		},
		"Usage": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt_tokens":     map[string]any{"type": "integer"},
				"completion_tokens": map[string]any{"type": "integer"},
				"total_tokens":      map[string]any{"type": "integer"},
			},
		},
		"RequestEnvelope": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model":       map[string]any{"type": "string", "description": "Model name or 'auto'"},
				"messages":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Message"}},
				"stream":      map[string]any{"type": "boolean"},
				"tools":       map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Tool"}},
				"tool_choice": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object"}}},
				"temperature": map[string]any{"type": "number", "minimum": 0, "maximum": 2},
				"max_tokens":  map[string]any{"type": "integer", "minimum": 1},
				"top_p":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"n":           map[string]any{"type": "integer", "minimum": 1},
				"user":        map[string]any{"type": "string"},
			},
			"required": []string{"model", "messages"},
		},
		"ResponseEnvelope": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string"},
				"object":  map[string]any{"type": "string"},
				"created": map[string]any{"type": "integer"},
				"model":   map[string]any{"type": "string"},
				"choices": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"index":         map[string]any{"type": "integer"},
							"message":       map[string]any{"$ref": "#/$defs/Message"},
							"finish_reason": map[string]any{"type": "string"},
						},
					},
				},
				"usage": map[string]any{"$ref": "#/$defs/Usage"},
			},
		},
		"EmbeddingRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model":           map[string]any{"type": "string"},
				"input":           map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
				"encoding_format": map[string]any{"type": "string", "enum": []string{"float", "base64"}},
				"dimensions":      map[string]any{"type": "integer"},
			},
			"required": []string{"model", "input"},
		},
		"ImageRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model":           map[string]any{"type": "string"},
				"prompt":          map[string]any{"type": "string"},
				"n":               map[string]any{"type": "integer"},
				"size":            map[string]any{"type": "string"},
				"quality":         map[string]any{"type": "string", "enum": []string{"standard", "hd"}},
				"response_format": map[string]any{"type": "string", "enum": []string{"url", "b64_json"}},
			},
			"required": []string{"model", "prompt"},
		},
	},
	"paths": map[string]any{
		"/v1/chat/completions": map[string]any{
			"post": map[string]any{
				"requestBody":  map[string]any{"$ref": "#/$defs/RequestEnvelope"},
				"responseBody": map[string]any{"$ref": "#/$defs/ResponseEnvelope"},
			},
		},
		"/v1/embeddings": map[string]any{
			"post": map[string]any{
				"requestBody": map[string]any{"$ref": "#/$defs/EmbeddingRequest"},
			},
		},
		"/v1/images/generations": map[string]any{
			"post": map[string]any{
				"requestBody": map[string]any{"$ref": "#/$defs/ImageRequest"},
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
