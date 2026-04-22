package team

import (
	"errors"
	"fmt"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Pipeline builds an actor.Spec for a coordinator that feeds each stage's
// Response.Message.Content into the next stage as a fresh user Prompt.
// From the outside the Pipeline behaves like a single Agent: Ask it with
// agent.Prompt and read back agent.Response / agent.Error.
//
// Streaming: when the caller's Prompt.Stream is non-nil, only the final
// stage receives the stream — intermediate stages are non-streaming so
// the caller's channel shows the user-visible answer without the noise
// of intermediate reasoning.
//
// Pipeline panics if no stages are provided.
func Pipeline(name string, stages ...actor.Ref) actor.Spec {
	if len(stages) == 0 {
		panic("team.Pipeline requires at least one stage")
	}
	ordered := append([]actor.Ref(nil), stages...)
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			return &pipelineActor{stages: ordered}
		},
	}
}

type pipelineActor struct {
	stages []actor.Ref
}

func (p *pipelineActor) Receive(actx actor.Context, msg any) error {
	prompt, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("team: Pipeline expects agent.Prompt, got %T", msg)
	}

	// The final stage owns closing Prompt.Stream (Agent.handlePrompt does
	// so in its defer). If we bail before reaching it, we close here so
	// the caller's range loop unblocks.
	streamHandedOff := false
	defer func() {
		if prompt.Stream != nil && !streamHandedOff {
			close(prompt.Stream)
		}
	}()

	total := llm.Usage{}
	input := prompt.Text
	var lastMsg llm.Message

	for i, stage := range p.stages {
		stagePrompt := agent.Prompt{Text: input}
		if i == len(p.stages)-1 {
			stagePrompt.Stream = prompt.Stream
			streamHandedOff = prompt.Stream != nil
		}
		reply, err := stage.Ask(actx, stagePrompt)
		if err != nil {
			return p.respondErr(actx, fmt.Errorf("team: stage %d ask: %w", i, err))
		}
		switch r := reply.(type) {
		case agent.Response:
			lastMsg = r.Message
			input = r.Message.Content
			if r.Usage != nil {
				total.PromptTokens += r.Usage.PromptTokens
				total.CompletionTokens += r.Usage.CompletionTokens
				total.TotalTokens += r.Usage.TotalTokens
			}
		case agent.Error:
			return p.respondErr(actx, fmt.Errorf("team: stage %d: %w", i, r.Err))
		default:
			return p.respondErr(actx, fmt.Errorf("team: stage %d unexpected reply %T", i, reply))
		}
	}

	if lastMsg.Role == "" {
		return p.respondErr(actx, errors.New("team: pipeline produced empty response"))
	}

	var usagePtr *llm.Usage
	if total != (llm.Usage{}) {
		u := total
		usagePtr = &u
	}
	return p.reply(actx, agent.Response{Message: lastMsg, Usage: usagePtr})
}

func (p *pipelineActor) respondErr(actx actor.Context, err error) error {
	actx.Log().Warn("team pipeline failed", "err", err.Error())
	return p.reply(actx, agent.Error{Err: err})
}

func (p *pipelineActor) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("team pipeline response dropped", "err", err.Error())
	}
	return nil
}
