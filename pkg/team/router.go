package team

import (
	"fmt"
	"hash/fnv"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
)

// KeyFunc extracts a routing key from a Prompt. An empty key routes to
// member 0.
type KeyFunc func(agent.Prompt) string

// Router builds an actor.Spec for a coordinator that dispatches each
// agent.Prompt to exactly one member chosen by stable key-based hashing.
//
// Dispatch algorithm: idx = FNV-64a(key) % len(members). An empty key
// (KeyFunc returns "") is treated as hash 0, so it always routes to
// member 0. The mapping is stable within a single process but is NOT
// consistent hashing for clusters — a change in len(members) will
// reassign keys. Cluster-aware consistent hashing is planned for M4.
//
// Router panics if members is empty or keyFn is nil.
func Router(name string, keyFn KeyFunc, members ...actor.Ref) actor.Spec {
	if keyFn == nil {
		panic("team.Router: keyFn must not be nil")
	}
	if len(members) == 0 {
		panic("team.Router requires at least one member")
	}
	copied := append([]actor.Ref(nil), members...)
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			return &routerActor{keyFn: keyFn, members: copied}
		},
	}
}

type routerActor struct {
	keyFn   KeyFunc
	members []actor.Ref
}

func (r *routerActor) Receive(actx actor.Context, msg any) error {
	prompt, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("team: Router expects agent.Prompt, got %T", msg)
	}

	key := r.keyFn(prompt)
	idx := routerIndex(key, len(r.members))
	chosen := r.members[idx]

	// If routing fails before Ask, we own closing the stream.
	// (In this implementation Ask is the only point of failure after
	// choosing the member, but the pattern matches Pipeline for safety.)
	streamHandedOff := false
	defer func() {
		if prompt.Stream != nil && !streamHandedOff {
			close(prompt.Stream)
		}
	}()

	// Forward the prompt as-is; the chosen member owns closing Stream.
	fwdPrompt := agent.Prompt{
		Text:   prompt.Text,
		Role:   prompt.Role,
		Stream: prompt.Stream,
	}
	streamHandedOff = prompt.Stream != nil

	reply, err := chosen.Ask(actx, fwdPrompt)
	if err != nil {
		streamHandedOff = false // re-close on error path
		return r.respondErr(actx, fmt.Errorf("team: router member %d ask: %w", idx, err))
	}

	switch rv := reply.(type) {
	case agent.Response:
		return r.reply(actx, rv)
	case agent.Error:
		return r.reply(actx, agent.Error{
			Err: fmt.Errorf("team: router member %d: %w", idx, rv.Err),
		})
	default:
		return r.respondErr(actx, fmt.Errorf("team: router member %d unexpected reply %T", idx, reply))
	}
}

// routerIndex computes the stable member index for key using FNV-64a.
// An empty key returns 0 (hash 0 % n == 0 for any n >= 1).
func routerIndex(key string, n int) int {
	if key == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(n))
}

func (r *routerActor) respondErr(actx actor.Context, err error) error {
	actx.Log().Warn("team router failed", "err", err.Error())
	return r.reply(actx, agent.Error{Err: err})
}

func (r *routerActor) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("team router response dropped", "err", err.Error())
	}
	return nil
}
