package retrieval

import (
	"encoding/json"
	"fmt"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// ToolName is the synthetic tool name injected into a request whenever
// content was compressed with reversibility enabled. It's intercepted by
// name in internal/ingress/chat.go — never passed through to the caller.
const ToolName = "ixr_retrieve"

// Tool returns the synthetic tool definition to inject into
// RequestEnvelope.Tools whenever at least one message was truncated with
// reversibility enabled.
func Tool() schema.Tool {
	return schema.Tool{
		Type: "function",
		Function: schema.FunctionDef{
			Name:        ToolName,
			Description: "Retrieve the full, uncompressed content that was shortened for context efficiency. Only call this if the shortened version doesn't have enough detail to answer accurately — most of the time it does, and calling this costs an extra round trip.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "The retrieval ID from the truncation marker, e.g. \"ret_42\".",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

// Marker returns the text appended after truncated content, telling the
// model how many characters were cut and how to get them back.
func Marker(id string, omittedChars int) string {
	return fmt.Sprintf("\n[%d more characters omitted — call %s with id=%q to retrieve the full content]", omittedChars, ToolName, id)
}

// ParseArgs extracts the "id" argument from a ToolCall's JSON arguments
// string. Returns ok=false on any malformed or missing id — callers should
// treat that as "can't resolve this call" rather than error out.
func ParseArgs(argsJSON string) (id string, ok bool) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.ID == "" {
		return "", false
	}
	return args.ID, true
}

// FindCall returns the first ToolCall in calls whose function name is
// ToolName, or ok=false if there isn't one.
func FindCall(calls []schema.ToolCall) (schema.ToolCall, bool) {
	for _, c := range calls {
		if c.Function.Name == ToolName {
			return c, true
		}
	}
	return schema.ToolCall{}, false
}
