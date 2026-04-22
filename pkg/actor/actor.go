// Package actor is the core of tinyagents: Actor, Ref, Context, Spec, and System.
//
// An Actor is a behavior driven by messages. Each actor runs on its own
// goroutine, reads one message at a time from its mailbox, and is never
// shared across goroutines. State inside an Actor therefore does not need
// synchronization.
//
// An actor is addressed only through its Ref, which is an opaque handle that
// can cross goroutines and — once clustering lands — nodes. Tell is
// fire-and-forget, Ask is request/reply with a context-bound timeout.
package actor

// Actor is the unit of behavior. Implementations should be cheap to construct
// since they are instantiated once per spawn and once per supervisor-driven
// restart.
type Actor interface {
	Receive(ctx Context, msg any) error
}

// ActorFunc adapts a plain function to the Actor interface for small actors
// that do not need their own struct.
type ActorFunc func(ctx Context, msg any) error

// Receive implements Actor.
func (f ActorFunc) Receive(ctx Context, msg any) error { return f(ctx, msg) }
