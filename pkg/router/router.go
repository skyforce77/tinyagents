// Package router fronts a pool of worker actors with a single dispatcher
// actor. User code sends messages to the router's Ref; the router forwards
// each message to one (or all) workers according to the configured Kind.
//
// Routers are themselves actors: they spawn their workers as children the
// first time a message arrives, which means stopping the router's Ref
// cascades stop through its context to every worker.
package router

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"

	"github.com/skyforce77/tinyagents/pkg/actor"
)

// Kind selects the routing strategy.
type Kind int

const (
	// RoundRobin dispatches messages in a rotating sequence.
	RoundRobin Kind = iota + 1
	// Random picks a worker uniformly.
	Random
	// Broadcast sends each message to every worker.
	Broadcast
	// ConsistentHash routes by a hash key extracted from the message, so
	// messages with the same key land on the same worker.
	ConsistentHash
	// LeastLoaded chooses the worker whose mailbox backlog is smallest,
	// falling back to RoundRobin when no worker reports load.
	LeastLoaded
)

// HashKeyer is implemented by messages that carry their own routing key.
// Routers in ConsistentHash mode call HashKey() if the message satisfies
// this interface, otherwise they fall back to Config.HashKey.
type HashKeyer interface {
	HashKey() string
}

// Config configures optional routing behavior.
type Config struct {
	// HashKey is used by ConsistentHash when the message does not
	// implement HashKeyer. Defaults to fmt.Sprint(msg).
	HashKey func(msg any) string
}

// Spawn creates poolSize worker actors (each built via spec.Factory and sharing
// spec.Mailbox / spec.Supervisor) and returns a Ref that dispatches to them
// per kind. The returned Ref addresses the router actor; workers live as its
// children so that stopping the router stops the whole pool.
func Spawn(sys *actor.System, spec actor.Spec, kind Kind, poolSize int, cfg Config) (actor.Ref, error) {
	if sys == nil {
		return nil, errors.New("router: System is required")
	}
	if poolSize < 1 {
		return nil, errors.New("router: poolSize must be >= 1")
	}
	if spec.Factory == nil {
		return nil, errors.New("router: Spec.Factory is required")
	}

	workerTemplate := spec
	workerTemplate.Name = "" // per-worker names assigned in init

	routerSpec := actor.Spec{
		Name: spec.Name,
		Factory: func() actor.Actor {
			return &routingActor{
				kind:       kind,
				cfg:        cfg,
				workerSpec: workerTemplate,
				poolSize:   poolSize,
			}
		},
		Mailbox: spec.Mailbox,
	}
	return sys.Spawn(routerSpec)
}

// routingActor is the dispatcher sitting in front of the worker pool.
// It is recreated on supervisor-driven restart, at which point it respawns
// its children; the previous children are already stopped because their
// context cancels on the router's failure.
type routingActor struct {
	kind       Kind
	cfg        Config
	workerSpec actor.Spec
	poolSize   int

	workers []actor.Ref
	rr      int
}

func (r *routingActor) Receive(ctx actor.Context, msg any) error {
	if len(r.workers) == 0 {
		if err := r.initWorkers(ctx); err != nil {
			return fmt.Errorf("router: init workers: %w", err)
		}
	}
	return r.dispatch(ctx, msg)
}

func (r *routingActor) initWorkers(ctx actor.Context) error {
	r.workers = make([]actor.Ref, 0, r.poolSize)
	for i := 0; i < r.poolSize; i++ {
		ws := r.workerSpec
		ws.Name = fmt.Sprintf("w%d", i)
		w, err := ctx.Spawn(ws)
		if err != nil {
			return err
		}
		r.workers = append(r.workers, w)
	}
	return nil
}

func (r *routingActor) dispatch(ctx actor.Context, msg any) error {
	switch r.kind {
	case RoundRobin:
		w := r.workers[r.rr%len(r.workers)]
		r.rr++
		return forwardLogging(ctx, w, msg)
	case Random:
		w := r.workers[rand.IntN(len(r.workers))]
		return forwardLogging(ctx, w, msg)
	case Broadcast:
		for _, w := range r.workers {
			_ = forwardLogging(ctx, w, msg)
		}
		return nil
	case ConsistentHash:
		key := r.keyFor(msg)
		idx := hashIndex(key, len(r.workers))
		return forwardLogging(ctx, r.workers[idx], msg)
	case LeastLoaded:
		return forwardLogging(ctx, r.pickLeastLoaded(), msg)
	default:
		return fmt.Errorf("router: unknown kind %d", r.kind)
	}
}

func (r *routingActor) keyFor(msg any) string {
	if k, ok := msg.(HashKeyer); ok {
		return k.HashKey()
	}
	if r.cfg.HashKey != nil {
		return r.cfg.HashKey(msg)
	}
	return fmt.Sprint(msg)
}

func (r *routingActor) pickLeastLoaded() actor.Ref {
	var best actor.Ref
	bestLoad := -1
	for _, w := range r.workers {
		lr, ok := w.(actor.LoadReporter)
		if !ok {
			continue
		}
		load := lr.Load()
		if bestLoad == -1 || load < bestLoad {
			bestLoad = load
			best = w
		}
	}
	if best == nil {
		// Fallback: round-robin-ish choice.
		best = r.workers[r.rr%len(r.workers)]
		r.rr++
	}
	return best
}

// forwardLogging emits a warning on forward failure but does not return the
// error: a transient worker problem (full mailbox, closed after restart)
// should not take down the router through supervisor-driven restart.
func forwardLogging(ctx actor.Context, target actor.Ref, msg any) error {
	if err := ctx.Forward(target, msg); err != nil {
		ctx.Log().Warn("router forward failed", "target", target.PID().String(), "err", err)
	}
	return nil
}

func hashIndex(key string, mod int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(mod))
}
