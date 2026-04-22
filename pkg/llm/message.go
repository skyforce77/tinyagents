// Package llm defines the provider-neutral chat, streaming, embedding, and
// tool-calling shapes that every adapter (openai, anthropic, ollama, …)
// converges on. Each agent holds its own Provider instance, so a single
// process can drive Ollama, Anthropic, OpenAI, and Mistral simultaneously
// without any provider becoming a singleton.
package llm

import "encoding/json"

// Role identifies the author of a Message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one element of a chat transcript. Content carries free-form
// text; ToolCalls is populated on assistant messages that requested tool
// invocation, and ToolCallID identifies the originating call on tool-role
// messages that report back the result.
type Message struct {
	Role       Role
	Content    string
	Name       string     // optional user handle or tool name
	ToolCalls  []ToolCall // assistant-side: tools the model wants invoked
	ToolCallID string     // tool-side: which call this is a response to
}

// ToolCall is a function-call request emitted by the assistant. Arguments
// is the provider's own JSON string; callers parse it against the tool's
// schema before invoking the tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec advertises a callable tool to the provider. Schema is a JSON
// Schema document describing the arguments object.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}
