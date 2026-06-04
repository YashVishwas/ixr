package schema

// ── Tool definitions (sent in the request) ────────────────────────────────────

// Tool is a tool the model may call.
type Tool struct {
	Type     string       `json:"type"`     // always "function"
	Function FunctionDef  `json:"function"`
}

// FunctionDef describes a callable function.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // JSON Schema object
	Strict      bool           `json:"strict,omitempty"`
}

// ToolChoiceString is a string-valued tool_choice ("none" | "auto" | "required").
type ToolChoiceString = string

// ToolChoiceObject forces a specific function call.
type ToolChoiceObject struct {
	Type     string              `json:"type"` // "function"
	Function ToolChoiceFunction  `json:"function"`
}

// ToolChoiceFunction names the function to force.
type ToolChoiceFunction struct {
	Name string `json:"name"`
}

// ── Tool call results (sent by the model in the response) ────────────────────

// ToolCall represents a single tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the name and JSON-encoded arguments for a tool call.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResult is the caller's response to a ToolCall (role="tool" message).
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}
