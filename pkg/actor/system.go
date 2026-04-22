package actor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/skyforce77/tinyagents/pkg/mailbox"
	"github.com/skyforce77/tinyagents/pkg/supervisor"
)

// System owns a pool of actors, a shared global store, and the root context
// that every actor derives its lifecycle from. One System per node.
type System struct {
	name   string
	logger *slog.Logger

	rootCtx    context.Context
	rootCancel context.CancelFunc

	globalStore *globalStore

	mu     sync.Mutex
	actors map[PID]*actorRuntime

	nextID atomic.Uint64
}

// Option configures a System at construction time.
type Option func(*System)

// WithLogger overrides the default slog logger.
func WithLogger(l *slog.Logger) Option { return func(s *System) { s.logger = l } }

// NewSystem builds a System. name is used as the PID node segment and in log
// messages; it has no network meaning until clustering is wired in.
func NewSystem(name string, opts ...Option) *System {
	ctx, cancel := context.WithCancel(context.Background())
	s := &System{
		name:        name,
		logger:      slog.New(slog.NewTextHandler(os.Stderr, nil)).With("system", name),
		rootCtx:     ctx,
		rootCancel:  cancel,
		globalStore: newGlobalStore(),
		actors:      map[PID]*actorRuntime{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name returns the system name (also the PID node segment).
func (s *System) Name() string { return s.name }

// Logger returns the system-wide logger.
func (s *System) Logger() *slog.Logger { return s.logger }

// Global is the system-wide store exposed to every actor via Context.Global().
func (s *System) Global() Store { return s.globalStore }

// Spawn creates a top-level actor under the "/user" namespace.
func (s *System) Spawn(spec Spec) (Ref, error) {
	return s.spawn(nil, "/user", spec)
}

// spawnChild is invoked from Context.Spawn; the parent becomes the child's
// PID prefix and (once supervision is hierarchical) its escalation target.
func (s *System) spawnChild(parent *actorRuntime, spec Spec) (Ref, error) {
	return s.spawn(parent, parent.pid.Path, spec)
}

func (s *System) spawn(parent *actorRuntime, prefix string, spec Spec) (Ref, error) {
	if err := s.rootCtx.Err(); err != nil {
		return nil, fmt.Errorf("actor: system stopped: %w", err)
	}
	if spec.Factory == nil {
		return nil, errors.New("actor: Spec.Factory is required")
	}

	name := spec.Name
	if name == "" {
		name = fmt.Sprintf("$%d", s.nextID.Add(1))
	}
	pid := PID{Node: s.name, Path: prefix + "/" + name}

	s.mu.Lock()
	if _, taken := s.actors[pid]; taken {
		s.mu.Unlock()
		return nil, fmt.Errorf("actor: %s already spawned", pid)
	}

	strategy := spec.Supervisor
	if strategy == nil {
		strategy = supervisor.Default()
	}

	// Children inherit from their parent so stopping the parent cascades.
	var actorCtx context.Context
	var cancel context.CancelFunc
	if parent != nil {
		actorCtx, cancel = context.WithCancel(parent.ctx)
	} else {
		actorCtx, cancel = context.WithCancel(s.rootCtx)
	}

	rt := &actorRuntime{
		system:     s,
		pid:        pid,
		spec:       spec,
		actor:      spec.Factory(),
		localStore: newLocalStore(),
		mailbox:    mailbox.New(spec.Mailbox),
		ctx:        actorCtx,
		cancel:     cancel,
		logger:     s.logger.With("pid", pid.String()),
		strategy:   strategy,
		stoppedCh:  make(chan struct{}),
	}
	rt.ref = &localRef{pid: pid, runtime: rt}
	s.actors[pid] = rt
	s.mu.Unlock()

	go rt.run()
	return rt.ref, nil
}

// unregister is called from an actor's terminate() defer.
func (s *System) unregister(pid PID) {
	s.mu.Lock()
	delete(s.actors, pid)
	s.mu.Unlock()
}

// Stop asks every actor to finish its current message, then waits for each
// runtime to exit. If ctx cancels first, Stop returns ctx.Err(); the system
// is then left in a partially-stopped state (all mailboxes closed but some
// Receive calls may still be in flight).
func (s *System) Stop(ctx context.Context) error {
	s.rootCancel()

	s.mu.Lock()
	runtimes := make([]*actorRuntime, 0, len(s.actors))
	for _, r := range s.actors {
		runtimes = append(runtimes, r)
	}
	s.mu.Unlock()

	for _, r := range runtimes {
		r.stop()
	}
	for _, r := range runtimes {
		select {
		case <-r.stoppedCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
