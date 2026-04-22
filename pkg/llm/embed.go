package llm

// EmbedRequest asks a provider to turn each string in Input into a vector.
// Model is optional — most providers pick a default embedding model.
type EmbedRequest struct {
	Model string
	Input []string
}

// EmbedResponse returns vectors in the same order as the request's Input.
type EmbedResponse struct {
	Vectors [][]float32
	Usage   *Usage
}

// Model is one entry in a Provider.Models() catalog.
type Model struct {
	ID              string
	ContextTokens   int
	MaxOutputTokens int
}
