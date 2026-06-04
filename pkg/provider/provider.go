// Package provider defines the interface for LLM backend adapters.
// Each provider (openai, anthropic, bedrock, ...) is a self-contained adapter
// that implements this interface. Adding a new provider never touches ixr core.
package provider

import (
	"context"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// StreamChunk is a single server-sent event chunk in canonical form.
// It maps the provider's SSE delta to a provider-agnostic shape.
type StreamChunk struct {
	ID           string
	Model        string
	Delta        schema.Message
	FinishReason string
	Usage        *schema.Usage // non-nil only on the final chunk
}

// Provider translates ixr's canonical schema to and from a specific LLM provider's
// wire format, and executes the call.
type Provider interface {
	// Name returns the provider identifier used in config and CallEvent.Provider.
	Name() string
	// Chat executes a chat completion request and returns the normalized response.
	Chat(ctx context.Context, req *schema.RequestEnvelope) (*schema.ResponseEnvelope, error)
	// Stream sends req with stream=true and calls fn for each chunk until [DONE] or error.
	// fn must not block. If fn returns an error, the stream is cancelled and Stream returns
	// that error. Context cancellation terminates the stream immediately.
	Stream(ctx context.Context, req *schema.RequestEnvelope, fn func(StreamChunk) error) error
}

// Embedder is implemented by providers that support the /v1/embeddings endpoint.
// Providers that don't support embeddings need not implement this interface.
type Embedder interface {
	Embed(ctx context.Context, req *schema.EmbeddingRequest) (*schema.EmbeddingResponse, error)
}

// ImageGenerator is implemented by providers that support /v1/images/generations.
type ImageGenerator interface {
	GenerateImage(ctx context.Context, req *schema.ImageRequest) (*schema.ImageResponse, error)
}
