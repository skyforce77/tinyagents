package actor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/skyforce77/tinyagents/pkg/mailbox"
)

// Context is handed to an Actor on every Receive. It embeds context.Context
// so cancellation and deadlines propagate down through Spawn, Ask, and any
// I/O the actor performs.
type Context interface {
	context.Context

	Self() Ref
	Sender() Ref // nil if message came from outside the actor system
	System() *System

	Store() Store  // per-actor state (single-goroutine access)
	Global() Store // system-wide state (concurrent-safe)
	Log() *slog.Logger

	Spawn(spec Spec) (Ref, error)

	// Tell sends msg to target with this actor as the sender.
	Tell(target Ref, msg any) error
	// Forward resends msg to target while preserving the original Sender.
	Forward(target Ref, msg any) error
	// Respond replies to the current Sender (or the Ask reply channel).
	Respond(msg any) error
}

// actorContext is the concrete Context given to one Receive call. It is
// short-lived: a new instance is created per message so Sender and ReplyTo
// always match the envelope being processed.
type actorContext struct {
	parent  context.Context
	runtime *actorRuntime
	sender  Ref
	replyTo any
}

func newActorContext(r *actorRuntime, env mailbox.Envelope) *actorContext {
	var sender Ref
	if s, ok := env.Sender.(Ref); ok {
		sender = s
	}
	return &actorContext{
		parent:  r.ctx,
		runtime: r,
		sender:  sender,
		replyTo: env.ReplyTo,
	}
}

// context.Context methods — delegate to the actor runtime's context.
func (c *actorContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *actorContext) Done() <-chan struct{}       { return c.parent.Done() }
func (c *actorContext) Err() error                  { return c.parent.Err() }
func (c *actorContext) Value(k any) any             { return c.parent.Value(k) }

func (c *actorContext) Self() Ref         { return c.runtime.ref }
func (c *actorContext) Sender() Ref       { return c.sender }
func (c *actorContext) System() *System   { return c.runtime.system }
func (c *actorContext) Store() Store      { return c.runtime.localStore }
func (c *actorContext) Global() Store     { return c.runtime.system.globalStore }
func (c *actorContext) Log() *slog.Logger { return c.runtime.logger }

func (c *actorContext) Spawn(spec Spec) (Ref, error) {
	return c.runtime.system.spawnChild(c.runtime, spec)
}

func (c *actorContext) Tell(target Ref, msg any) error {
	if target == nil {
		return errors.New("actor: Tell target is nil")
	}
	return target.tellFrom(msg, c.runtime.ref)
}

func (c *actorContext) Forward(target Ref, msg any) error {
	if target == nil {
		return errors.New("actor: Forward target is nil")
	}
	return target.tellFrom(msg, c.sender)
}

func (c *actorContext) Respond(msg any) error {
	if c.replyTo != nil {
		switch rt := c.replyTo.(type) {
		case chan any:
			select {
			case rt <- msg:
				return nil
			default:
				return errors.New("actor: reply channel full or closed")
			}
		case Ref:
			return rt.tellFrom(msg, c.runtime.ref)
		}
	}
	if c.sender != nil {
		return c.sender.tellFrom(msg, c.runtime.ref)
	}
	return errors.New("actor: no reply destination")
}
