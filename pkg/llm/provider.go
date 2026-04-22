package llm

import "context"

// Provider is the one interface every adapter satisfies. Implementations
// must be safe for concurrent use — a single instance can back many agents.
//
// Stream returns a channel the caller must drain (or cancel the context to
// abandon). The channel is closed by the adapter after emitting the
// terminal Chunk.
type Provider interface {
	// Name is a short identifier used in logs and in Registry lookups
	// (e.g. "openai", "anthropic", "ollama").
	Name() string

	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
	Models(ctx context.Context) ([]Model, error)
}
