package llm

// ChatRequest is the unified shape every adapter accepts. Pointer fields
// mean "unset"; the adapter either passes the provider's default or omits
// the parameter entirely.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolSpec
	Temperature *float64
	MaxTokens   *int
	Stop        []string

	// Metadata carries arbitrary string tags forwarded to providers that
	// support them (idempotency-key, user id, trace id, …) and logged
	// locally otherwise.
	Metadata map[string]string
}

// ChatResponse is the outcome of a non-streaming Chat call.
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        *Usage
}

// Chunk is one frame of a streaming response.
//
// Semantics:
//   - Delta carries incremental text. Append across chunks to build the final
//     Content.
//   - ToolCall is set only once per tool invocation, on the chunk where the
//     adapter has aggregated enough partial data to emit a complete call.
//   - FinishReason and Usage are set on the terminal chunk for that turn.
//
// The channel closes after the terminal chunk.
type Chunk struct {
	Delta        string
	ToolCall     *ToolCall
	FinishReason string
	Usage        *Usage
}

// Usage reports token accounting. All fields are optional; providers that
// don't expose counts leave them zero.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
