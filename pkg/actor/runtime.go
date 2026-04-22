package actor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/skyforce77/tinyagents/pkg/mailbox"
	"github.com/skyforce77/tinyagents/pkg/supervisor"
)

// actorRuntime is the per-actor bookkeeping object: lifecycle, mailbox,
// supervision state, and the goroutine that drains messages.
type actorRuntime struct {
	system *System
	pid    PID
	spec   Spec

	actor      Actor
	localStore *localStore
	mailbox    mailbox.Mailbox

	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger

	strategy     supervisor.Strategy
	failureCount int       // touched only from the run goroutine
	windowStart  time.Time // touched only from the run goroutine

	ref *localRef

	stopOnce  sync.Once
	stoppedCh chan struct{}
}

// run is the actor's single-goroutine loop.
func (r *actorRuntime) run() {
	defer r.terminate()
	for {
		env, err := r.mailbox.Dequeue(r.ctx)
		if err != nil {
			return
		}
		if !r.dispatch(env) {
			return
		}
	}
}

// dispatch processes one envelope. It returns false when the actor must stop
// (either supervisor said so or the context was cancelled during a restart
// backoff).
func (r *actorRuntime) dispatch(env mailbox.Envelope) (keepRunning bool) {
	keepRunning = true
	defer func() {
		if rec := recover(); rec != nil {
			keepRunning = r.handleFailure(rec)
		}
	}()

	actx := newActorContext(r, env)
	if err := r.actor.Receive(actx, env.Msg); err != nil {
		keepRunning = r.handleFailure(err)
		return
	}
	// Successful message: forgive accumulated failures.
	r.failureCount = 0
	r.windowStart = time.Time{}
	return
}

func (r *actorRuntime) handleFailure(cause any) bool {
	if r.failureCount == 0 {
		r.windowStart = time.Now()
	}
	r.failureCount++
	decision := r.strategy.Decide(cause, r.failureCount, r.windowStart)
	r.logger.Warn("actor failure",
		"pid", r.pid.String(),
		"cause", fmt.Sprint(cause),
		"attempt", r.failureCount,
		"directive", directiveString(decision.Directive),
	)
	switch decision.Directive {
	case supervisor.Resume:
		return true
	case supervisor.Restart:
		if decision.Delay > 0 {
			t := time.NewTimer(decision.Delay)
			select {
			case <-t.C:
			case <-r.ctx.Done():
				t.Stop()
				return false
			}
		}
		r.actor = r.spec.Factory()
		return true
	case supervisor.Stop, supervisor.Escalate:
		fallthrough
	default:
		return false
	}
}

func directiveString(d supervisor.Directive) string {
	switch d {
	case supervisor.Resume:
		return "resume"
	case supervisor.Restart:
		return "restart"
	case supervisor.Stop:
		return "stop"
	case supervisor.Escalate:
		return "escalate"
	default:
		return "unknown"
	}
}

// stop is safe to call multiple times; only the first invocation actually
// cancels the context and closes the mailbox.
func (r *actorRuntime) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		_ = r.mailbox.Close()
	})
}

// terminate runs from the run goroutine's defer. It unregisters from the
// system and signals any waiter on stoppedCh.
func (r *actorRuntime) terminate() {
	r.stop() // ensure mailbox closed + ctx cancelled
	r.system.unregister(r.pid)
	close(r.stoppedCh)
}
