package ollama

import "encoding/json"

// The JSON shapes below mirror the Ollama REST API. They are intentionally
// isolated from the public llm types so the adapter can evolve its wire
// format without leaking detail upward.

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Tools    []toolSpec     `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	Format   string         `json:"format,omitempty"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolSpec struct {
	Type     string          `json:"type"` // always "function" today
	Function toolSpecFn      `json:"function"`
}

type toolSpecFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatResponse struct {
	Model           string      `json:"model"`
	Message         chatMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float32 `json:"embeddings"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

type tagsResponse struct {
	Models []tagsModel `json:"models"`
}

type tagsModel struct {
	Name string `json:"name"`
}
