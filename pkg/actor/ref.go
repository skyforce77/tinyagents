package actor

import (
	"context"
	"errors"

	"github.com/skyforce77/tinyagents/pkg/mailbox"
)

// Ref is an opaque handle to an actor. It is safe to share across goroutines
// and — in a future cluster build — across nodes. Concrete implementations
// are supplied by the actor package; users never construct a Ref directly.
type Ref interface {
	PID() PID
	Tell(msg any) error
	Ask(ctx context.Context, msg any) (any, error)
	Stop() error

	// tellFrom is the unexported hook used by actorContext to propagate the
	// current actor as the message sender. Keeping this unexported means
	// only the actor package can produce "from" semantics, which keeps the
	// public Ref surface minimal.
	tellFrom(msg any, sender Ref) error
}

// localRef points to a same-process actorRuntime. When clustering lands a
// remoteRef will implement the same interface and forward through the
// transport package.
type localRef struct {
	pid     PID
	runtime *actorRuntime
}

func (r *localRef) PID() PID { return r.pid }

func (r *localRef) Tell(msg any) error {
	return r.deliver(mailbox.Envelope{Msg: msg})
}

func (r *localRef) tellFrom(msg any, sender Ref) error {
	return r.deliver(mailbox.Envelope{Msg: msg, Sender: sender})
}

func (r *localRef) Ask(ctx context.Context, msg any) (any, error) {
	reply := make(chan any, 1)
	if err := r.deliver(mailbox.Envelope{Msg: msg, ReplyTo: reply}); err != nil {
		return nil, err
	}
	select {
	case resp := <-reply:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *localRef) Stop() error {
	r.runtime.stop()
	return nil
}

// deliver enqueues env to the target's mailbox. Uses the actor's own context
// so Enqueue returns promptly if the actor is already stopping.
func (r *localRef) deliver(env mailbox.Envelope) error {
	if r.runtime == nil {
		return errors.New("actor: dead ref")
	}
	return r.runtime.mailbox.Enqueue(r.runtime.ctx, env)
}
