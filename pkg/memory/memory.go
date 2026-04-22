// Package memory defines the short/long-term memory contract that agents use.
// v1 ships one implementation under pkg/memory/buffer; vector and sqlite
// stores can land later without touching this interface.
package memory

import (
	"context"
	"errors"
)

// ErrNotSupported is returned by implementations that don't implement a
// particular method (e.g. Search on the ring buffer).
var ErrNotSupported = errors.New("memory: feature not supported")

// Message is one element of a chat transcript.
type Message struct {
	Role    string
	Content string
}

// Memory is the agent-facing short/long-term store.
type Memory interface {
	Append(ctx context.Context, m Message) error
	// Window returns the most recent n messages in chronological order (oldest
	// first). n == 0 means "everything". Implementations may cap n at their
	// own capacity.
	Window(ctx context.Context, n int) ([]Message, error)
	// Search is optional. If unsupported, return (nil, ErrNotSupported).
	Search(ctx context.Context, q string, k int) ([]Message, error)
}
