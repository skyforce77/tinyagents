package team

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Broadcast builds an actor.Spec for a coordinator that fans a single
// agent.Prompt out to every member in parallel, then joins all responses
// into one agent.Response. From the outside Broadcast behaves like a single
// Agent: Ask it with agent.Prompt and read back agent.Response (or
// agent.Error).
//
// The aggregated Response has:
//   - Message.Role = llm.RoleAssistant
//   - Message.Content = member responses joined by "\n---\n", in member order
//     (not arrival order)
//   - Usage = sum of every member's reported Usage; nil if none reported any
//
// Short-circuit on error: if any member replies with agent.Error, Broadcast
// immediately returns an agent.Error that wraps the member's error and
// includes the member index. In-flight Asks to other members are cancelled
// via the context derived from actor.Context.
//
// Streaming: Broadcast does not support streaming in v1. A Prompt with a
// non-nil Stream is rejected with agent.Error and the stream channel is
// closed.
//
// Broadcast panics if no members are provided.
func Broadcast(name string, members ...actor.Ref) actor.Spec {
	if len(members) == 0 {
		panic("team.Broadcast requires at least one member")
	}
	ordered := append([]actor.Ref(nil), members...)
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			return &broadcastActor{members: ordered}
		},
	}
}

type broadcastActor struct {
	members []actor.Ref
}

// slot collects one member's outcome for fan-in.
type slot struct {
	idx  int
	resp agent.Response
	err  error
}

func (b *broadcastActor) Receive(actx actor.Context, msg any) error {
	prompt, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("team: Broadcast expects agent.Prompt, got %T", msg)
	}

	// Streaming is not supported in v1 — reject immediately.
	if prompt.Stream != nil {
		close(prompt.Stream)
		return b.respondErr(actx, errors.New("team: Broadcast does not support streaming in v1"))
	}

	// Derive a cancellable context so we can abort in-flight Asks on first
	// member error.
	ctx, cancel := context.WithCancel(actx)
	defer cancel()

	results := make(chan slot, len(b.members))

	for i, member := range b.members {
		i, member := i, member
		go func() {
			reply, err := member.Ask(ctx, agent.Prompt{Text: prompt.Text, Role: prompt.Role})
			if err != nil {
				results <- slot{idx: i, err: err}
				return
			}
			switch r := reply.(type) {
			case agent.Response:
				results <- slot{idx: i, resp: r}
			case agent.Error:
				results <- slot{idx: i, err: r.Err}
			default:
				results <- slot{idx: i, err: fmt.Errorf("unexpected reply type %T from member %d", reply, i)}
			}
		}()
	}

	// Collect all results; short-circuit on first error.
	slots := make([]slot, len(b.members))
	var firstErr error
	var firstErrIdx int
	for range b.members {
		s := <-results
		if s.err != nil && firstErr == nil {
			firstErr = s.err
			firstErrIdx = s.idx
			cancel() // signal remaining Asks to abort
		}
		slots[s.idx] = s
	}

	if firstErr != nil {
		return b.respondErr(actx, fmt.Errorf("team: member %d: %w", firstErrIdx, firstErr))
	}

	// Build ordered join.
	parts := make([]string, len(b.members))
	total := llm.Usage{}
	hasUsage := false
	for i, s := range slots {
		parts[i] = s.resp.Message.Content
		if s.resp.Usage != nil {
			total.PromptTokens += s.resp.Usage.PromptTokens
			total.CompletionTokens += s.resp.Usage.CompletionTokens
			total.TotalTokens += s.resp.Usage.TotalTokens
			hasUsage = true
		}
	}

	var usagePtr *llm.Usage
	if hasUsage {
		u := total
		usagePtr = &u
	}
	return b.reply(actx, agent.Response{
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: strings.Join(parts, "\n---\n"),
		},
		Usage: usagePtr,
	})
}

func (b *broadcastActor) respondErr(actx actor.Context, err error) error {
	actx.Log().Warn("team broadcast failed", "err", err.Error())
	return b.reply(actx, agent.Error{Err: err})
}

func (b *broadcastActor) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("team broadcast response dropped", "err", err.Error())
	}
	return nil
}
