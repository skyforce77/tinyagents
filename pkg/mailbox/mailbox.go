// Package mailbox defines the message queue that fronts every actor.
// Implementations are responsible for ordering, capacity, and backpressure.
package mailbox

import (
	"context"
	"errors"
)

// Envelope carries one message through the mailbox. The mailbox treats
// Sender and ReplyTo as opaque; the actor package is the only layer that
// interprets their concrete types.
type Envelope struct {
	Msg     any
	Sender  any
	ReplyTo any
	Headers map[string]string
}

// BackpressurePolicy selects how Enqueue behaves on a full bounded mailbox.
type BackpressurePolicy int

const (
	// Block waits until space is available or the context is cancelled.
	Block BackpressurePolicy = iota
	// DropNewest silently drops the incoming envelope and returns nil.
	DropNewest
	// DropOldest evicts the front of the queue to make room.
	DropOldest
	// Fail returns ErrMailboxFull.
	Fail
)

// Config controls the shape of a mailbox.
// Capacity == 0 means unbounded; Policy is ignored in that case.
type Config struct {
	Capacity int
	Policy   BackpressurePolicy
}

// Mailbox is the public contract every implementation must satisfy.
type Mailbox interface {
	Enqueue(ctx context.Context, env Envelope) error
	Dequeue(ctx context.Context) (Envelope, error)
	Len() int
	Cap() int
	Close() error
}

// Errors.
var (
	ErrMailboxFull   = errors.New("mailbox: full")
	ErrMailboxClosed = errors.New("mailbox: closed")
)

// New builds a mailbox from a Config. Capacity == 0 yields an unbounded
// mailbox; otherwise a bounded one honoring the given policy.
func New(cfg Config) Mailbox {
	if cfg.Capacity <= 0 {
		return newUnbounded()
	}
	return newBounded(cfg.Capacity, cfg.Policy)
}
