package schema

// EmbeddingRequest is the canonical form of a POST /v1/embeddings body.
type EmbeddingRequest struct {
	Model          string   `json:"model"`
	Input          any      `json:"input"`          // string or []string
	EncodingFormat string   `json:"encoding_format,omitempty"` // "float" | "base64"
	Dimensions     int      `json:"dimensions,omitempty"`
	User           string   `json:"user,omitempty"`
}

// EmbeddingObject is one embedding in the response.
type EmbeddingObject struct {
	Object    string    `json:"object"` // "embedding"
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingResponse is the canonical form of a /v1/embeddings response.
type EmbeddingResponse struct {
	Object string            `json:"object"` // "list"
	Data   []EmbeddingObject `json:"data"`
	Model  string            `json:"model"`
	Usage  EmbeddingUsage    `json:"usage"`
}

// EmbeddingUsage reports token consumption for an embedding call.
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
