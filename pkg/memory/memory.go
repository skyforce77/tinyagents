// Package memory defines the short/long-term memory contract that agents use.
// v1 ships one implementation under pkg/memory/buffer; vector and sqlite
// stores can land later without touching this interface.
package memory

import (
	"context"
	"errors"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// ErrNotSupported is returned by implementations that don't implement a
// particular method (e.g. Search on the ring buffer).
var ErrNotSupported = errors.New("memory: feature not supported")

// Memory is the agent-facing short/long-term store. Conversations are built
// from llm.Message values so the same data can flow straight into
// llm.ChatRequest.Messages without a conversion step.
type Memory interface {
	Append(ctx context.Context, m llm.Message) error
	// Window returns the most recent n messages in chronological order
	// (oldest first). n <= 0 means "everything". Implementations may cap n
	// at their own capacity.
	Window(ctx context.Context, n int) ([]llm.Message, error)
	// Search is optional. If unsupported, return (nil, ErrNotSupported).
	Search(ctx context.Context, q string, k int) ([]llm.Message, error)
}
