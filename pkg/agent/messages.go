package agent

import (
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Prompt is the primary request an Agent handles. Callers Tell or Ask an
// Agent with a Prompt; the Agent replies with a Response (or Error).
type Prompt struct {
	// Text becomes the Content of a user Message appended to the Agent's
	// transcript before the first provider call.
	Text string

	// Role overrides the role attached to Text. Zero value defaults to
	// llm.RoleUser. Useful for injecting system-level observations or
	// replaying tool results from outside the Agent.
	Role llm.Role

	// Stream, when non-nil, receives every llm.Chunk the provider emits,
	// including chunks from intermediate tool-use turns. The Agent closes
	// Stream before sending the final Response. A nil Stream means the
	// Agent uses Provider.Chat (non-streaming).
	Stream chan<- llm.Chunk
}

// Response is the terminal reply an Agent sends back to the Prompt sender.
type Response struct {
	// Message is the final assistant Message (no pending ToolCalls).
	Message llm.Message
	// Usage is the sum of Usage values reported across every provider call
	// made for this Prompt, including tool-use iterations. Nil if the
	// provider did not report usage.
	Usage *llm.Usage
}

// Error is delivered in place of a Response when the Agent cannot complete
// the Prompt (budget exhausted, provider error, unknown tool, max turns
// reached).
type Error struct {
	Err error
}

func (e Error) Error() string { return e.Err.Error() }
