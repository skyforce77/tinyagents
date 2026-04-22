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
	parent *actorRuntime // nil for top-level actors

	actor      Actor
	localStore *localStore
	mailbox    mailbox.Mailbox

	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger

	strategy     supervisor.Strategy
	failureCount int       // touched only from the run goroutine
	windowStart  time.Time // touched only from the run goroutine
	stopReason   any       // final cause, set before terminate()

	ref *localRef

	// watchMu guards watchers and watching. Both maps are manipulated from
	// multiple goroutines (ours and the watcher's / target's), so they need
	// explicit synchronization unlike the actor's in-receive state.
	watchMu  sync.Mutex
	watchers map[PID]*actorRuntime // actors that want a Terminated from us
	watching map[PID]*actorRuntime // actors we have subscribed to

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
	case supervisor.Escalate:
		if r.parent != nil {
			_ = r.parent.ref.tellFrom(Failed{Child: r.pid, Cause: cause}, r.ref)
		}
		r.stopReason = cause
		return false
	case supervisor.Stop:
		fallthrough
	default:
		r.stopReason = cause
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
// system, notifies watchers, tears down watch links, and signals stoppedCh.
func (r *actorRuntime) terminate() {
	r.stop() // ensure mailbox closed + ctx cancelled

	// Snapshot watchers while holding the lock, then notify them outside it
	// so their Tell doesn't wait on our lock.
	r.watchMu.Lock()
	watchers := make([]*actorRuntime, 0, len(r.watchers))
	for _, w := range r.watchers {
		watchers = append(watchers, w)
	}
	r.watchers = nil
	watching := r.watching
	r.watching = nil
	r.watchMu.Unlock()

	term := Terminated{PID: r.pid, Reason: r.stopReason}
	for _, w := range watchers {
		_ = w.ref.tellFrom(term, r.ref)
	}
	// Unsubscribe from anyone we were watching so they don't hold a stale
	// pointer to us (and don't try to notify us on their own termination).
	for _, target := range watching {
		target.removeWatcher(r.pid)
	}

	r.system.unregister(r.pid)
	close(r.stoppedCh)
}

// addWatcher / removeWatcher / addWatching / removeWatching are the small
// critical sections that keep the watch graph consistent across goroutines.

func (r *actorRuntime) addWatcher(w *actorRuntime) {
	r.watchMu.Lock()
	// If we're already terminating (watchers set cleared), signal the caller
	// immediately so the contract "watching a stopped Ref still delivers
	// Terminated" holds.
	if r.watchers == nil && r.isStopped() {
		r.watchMu.Unlock()
		_ = w.ref.tellFrom(Terminated{PID: r.pid, Reason: r.stopReason}, r.ref)
		return
	}
	if r.watchers == nil {
		r.watchers = map[PID]*actorRuntime{}
	}
	r.watchers[w.pid] = w
	r.watchMu.Unlock()
}

func (r *actorRuntime) removeWatcher(pid PID) {
	r.watchMu.Lock()
	delete(r.watchers, pid)
	r.watchMu.Unlock()
}

func (r *actorRuntime) addWatching(target *actorRuntime) {
	r.watchMu.Lock()
	if r.watching == nil {
		r.watching = map[PID]*actorRuntime{}
	}
	r.watching[target.pid] = target
	r.watchMu.Unlock()
}

func (r *actorRuntime) removeWatching(pid PID) {
	r.watchMu.Lock()
	delete(r.watching, pid)
	r.watchMu.Unlock()
}

func (r *actorRuntime) isStopped() bool {
	select {
	case <-r.stoppedCh:
		return true
	default:
		return false
	}
}
