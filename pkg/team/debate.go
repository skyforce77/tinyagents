package team

import (
	"fmt"
	"strings"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Debate runs `rounds` full rounds of round-robin turns between the
// debaters, then asks the arbiter to declare a verdict over the full
// transcript.
//
// Transcript format:
//
//	Topic: <topic>
//
//	Debater 0 (turn 1): <reply>
//	Debater 1 (turn 1): <reply>
//	...
//
// Streaming: only the arbiter's final call receives the caller's Stream.
// Intermediate debater turns always run non-streaming.
//
// Debate panics if arbiter is nil, len(debaters) < 2, or rounds < 1.
func Debate(name string, rounds int, arbiter actor.Ref, debaters ...actor.Ref) actor.Spec {
	if arbiter == nil {
		panic("team.Debate: arbiter must not be nil")
	}
	if len(debaters) < 2 {
		panic("team.Debate: at least 2 debaters required")
	}
	if rounds < 1 {
		panic("team.Debate: rounds must be >= 1")
	}
	copied := append([]actor.Ref(nil), debaters...)
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			return &debateActor{rounds: rounds, arbiter: arbiter, debaters: copied}
		},
	}
}

type debateActor struct {
	rounds   int
	arbiter  actor.Ref
	debaters []actor.Ref
}

func (d *debateActor) Receive(actx actor.Context, msg any) error {
	prompt, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("team: Debate expects agent.Prompt, got %T", msg)
	}

	// If we bail before handing Stream to the arbiter, close it so the
	// caller's range loop unblocks.
	streamHandedOff := false
	defer func() {
		if prompt.Stream != nil && !streamHandedOff {
			close(prompt.Stream)
		}
	}()

	var sb strings.Builder
	fmt.Fprintf(&sb, "Topic: %s", prompt.Text)

	total := llm.Usage{}

	for turn := 1; turn <= d.rounds; turn++ {
		for i, debater := range d.debaters {
			label := fmt.Sprintf("Debater %d (turn %d): ", i, turn)
			sb.WriteString("\n\n")
			sb.WriteString(label)

			dp := agent.Prompt{
				Text:   sb.String(),
				Role:   llm.RoleUser,
				Stream: nil,
			}
			raw, err := debater.Ask(actx, dp)
			if err != nil {
				role := fmt.Sprintf("debater %d turn %d", i, turn)
				actx.Log().Warn("team debate debater failed", "role", role, "err", err.Error())
				return d.respondErr(actx, fmt.Errorf("team: %s ask: %w", role, err))
			}
			switch r := raw.(type) {
			case agent.Response:
				sb.WriteString(r.Message.Content)
				if r.Usage != nil {
					total.PromptTokens += r.Usage.PromptTokens
					total.CompletionTokens += r.Usage.CompletionTokens
					total.TotalTokens += r.Usage.TotalTokens
				}
			case agent.Error:
				role := fmt.Sprintf("debater %d turn %d", i, turn)
				actx.Log().Warn("team debate debater error", "role", role, "err", r.Err.Error())
				return d.respondErr(actx, fmt.Errorf("team: %s: %w", role, r.Err))
			default:
				role := fmt.Sprintf("debater %d turn %d", i, turn)
				return d.respondErr(actx, fmt.Errorf("team: %s unexpected reply %T", role, raw))
			}
		}
	}

	// Ask the arbiter over the complete transcript.
	arbiterText := sb.String() + "\n\nAs the arbiter, analyze the debate and declare a verdict."
	arbiterPrompt := agent.Prompt{
		Text:   arbiterText,
		Role:   llm.RoleUser,
		Stream: prompt.Stream,
	}
	streamHandedOff = prompt.Stream != nil

	raw, err := d.arbiter.Ask(actx, arbiterPrompt)
	if err != nil {
		actx.Log().Warn("team debate arbiter failed", "err", err.Error())
		return d.respondErr(actx, fmt.Errorf("team: arbiter ask: %w", err))
	}
	switch r := raw.(type) {
	case agent.Response:
		if r.Usage != nil {
			total.PromptTokens += r.Usage.PromptTokens
			total.CompletionTokens += r.Usage.CompletionTokens
			total.TotalTokens += r.Usage.TotalTokens
		}
		var usagePtr *llm.Usage
		if total != (llm.Usage{}) {
			u := total
			usagePtr = &u
		}
		return d.reply(actx, agent.Response{Message: r.Message, Usage: usagePtr})
	case agent.Error:
		actx.Log().Warn("team debate arbiter error", "err", r.Err.Error())
		return d.respondErr(actx, fmt.Errorf("team: arbiter: %w", r.Err))
	default:
		return d.respondErr(actx, fmt.Errorf("team: arbiter unexpected reply %T", raw))
	}
}

func (d *debateActor) respondErr(actx actor.Context, err error) error {
	actx.Log().Warn("team debate failed", "err", err.Error())
	return d.reply(actx, agent.Error{Err: err})
}

func (d *debateActor) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("team debate response dropped", "err", err.Error())
	}
	return nil
}
