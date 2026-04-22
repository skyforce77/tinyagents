// Package agent wraps an LLM Provider, a tool registry, a short-term
// memory, and a Policy behind the actor.Actor interface. Every Agent owns
// its own Provider, so a single tinyagents System can drive Ollama,
// Anthropic, OpenAI, and Mistral concurrently — heterogeneous teams are
// first-class.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/memory"
	"github.com/skyforce77/tinyagents/pkg/memory/buffer"
	"github.com/skyforce77/tinyagents/pkg/tool"
)

// Agent is an actor that translates Prompt messages into LLM calls,
// executes any tool invocations the model requests, and responds with the
// final assistant Message.
type Agent struct {
	// ID is a human-readable label included in log lines and error
	// messages. It need not be unique; use the actor's PID for that.
	ID string

	// Provider is the LLM backend used for every call the Agent makes.
	// Required. Two Agents with different Providers can coexist in the
	// same System without conflict.
	Provider llm.Provider

	// Model is forwarded as ChatRequest.Model; adapters interpret the
	// string themselves (e.g. "gpt-4o-mini", "claude-sonnet-4-5",
	// "llama3.2:latest").
	Model string

	// System is an optional system-role prompt prepended to every request.
	System string

	// Tools are exposed to the model as callable functions. When a
	// response includes tool_calls, the Agent invokes the matching Tool
	// and appends its result to the transcript before calling the
	// provider again (up to Policy.MaxTurns).
	Tools []tool.Tool

	// Memory stores the conversation transcript across prompts. Nil
	// means stateless: the Agent creates an ephemeral ring per prompt
	// so tool-use loops still have context, but nothing persists
	// between prompts.
	Memory memory.Memory

	// Policy bounds token usage and tool-use depth.
	Policy Policy
}

// ephemeralBufferCap is the ring size used when Memory is nil. Large enough
// to carry one prompt through a multi-step tool loop.
const ephemeralBufferCap = 64

// Receive implements actor.Actor. Unknown message types are returned as
// errors so the supervisor can decide what to do with them.
func (a *Agent) Receive(actx actor.Context, msg any) error {
	switch m := msg.(type) {
	case Prompt:
		return a.handlePrompt(actx, m)
	default:
		return fmt.Errorf("agent %q: unknown message type %T", a.ID, msg)
	}
}

func (a *Agent) handlePrompt(actx actor.Context, p Prompt) error {
	if a.Provider == nil {
		return a.fail(actx, p, errors.New("agent: Provider is nil"))
	}
	if p.Stream != nil {
		defer close(p.Stream)
	}

	mem := a.Memory
	if mem == nil {
		mem = buffer.New(ephemeralBufferCap)
	}

	role := p.Role
	if role == "" {
		role = llm.RoleUser
	}
	userMsg := llm.Message{Role: role, Content: p.Text}
	if err := mem.Append(actx, userMsg); err != nil {
		return a.fail(actx, p, fmt.Errorf("agent: memory append: %w", err))
	}

	maxTurns := a.Policy.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	total := llm.Usage{}
	var final llm.Message
	done := false

	for turn := 0; turn < maxTurns && !done; turn++ {
		if a.Policy.Budget != nil {
			if err := a.Policy.Budget.Reserve(); err != nil {
				return a.fail(actx, p, err)
			}
		}

		req, err := a.buildRequest(actx, mem)
		if err != nil {
			return a.fail(actx, p, err)
		}

		var asst llm.Message
		var usage *llm.Usage
		if p.Stream != nil {
			asst, usage, err = a.runStream(actx, req, p.Stream)
		} else {
			var resp llm.ChatResponse
			resp, err = a.Provider.Chat(actx, req)
			asst, usage = resp.Message, resp.Usage
		}
		if err != nil {
			return a.fail(actx, p, fmt.Errorf("agent: provider call: %w", err))
		}

		accumulateUsage(&total, usage)
		if a.Policy.Budget != nil {
			if rerr := a.Policy.Budget.Record(usage); rerr != nil {
				return a.fail(actx, p, rerr)
			}
		}

		if asst.Role == "" {
			asst.Role = llm.RoleAssistant
		}
		if err := mem.Append(actx, asst); err != nil {
			return a.fail(actx, p, fmt.Errorf("agent: memory append: %w", err))
		}

		if len(asst.ToolCalls) == 0 {
			final = asst
			done = true
			break
		}

		for _, call := range asst.ToolCalls {
			toolMsg := a.runTool(actx, call)
			if err := mem.Append(actx, toolMsg); err != nil {
				return a.fail(actx, p, fmt.Errorf("agent: memory append: %w", err))
			}
		}
	}

	if !done {
		return a.fail(actx, p, fmt.Errorf("agent: max turns (%d) reached without final answer", maxTurns))
	}

	var usagePtr *llm.Usage
	if total != (llm.Usage{}) {
		u := total
		usagePtr = &u
	}
	return a.reply(actx, Response{Message: final, Usage: usagePtr})
}

// buildRequest assembles a ChatRequest from the current transcript plus the
// Agent's configuration. The System prompt (if any) is always prepended;
// Memory contributes the rest in chronological order.
func (a *Agent) buildRequest(ctx context.Context, mem memory.Memory) (llm.ChatRequest, error) {
	win, err := mem.Window(ctx, 0)
	if err != nil {
		return llm.ChatRequest{}, fmt.Errorf("agent: memory window: %w", err)
	}
	msgs := make([]llm.Message, 0, len(win)+1)
	if a.System != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: a.System})
	}
	msgs = append(msgs, win...)

	req := llm.ChatRequest{
		Model:    a.Model,
		Messages: msgs,
	}
	if len(a.Tools) > 0 {
		req.Tools = tool.Specs(a.Tools)
	}
	if a.Policy.Temperature != nil {
		t := *a.Policy.Temperature
		req.Temperature = &t
	}
	if a.Policy.MaxTokens > 0 {
		mt := a.Policy.MaxTokens
		req.MaxTokens = &mt
	}
	return req, nil
}

// runStream consumes a Provider.Stream channel, forwards every Chunk to
// out, and assembles the aggregated assistant Message + final Usage.
func (a *Agent) runStream(ctx context.Context, req llm.ChatRequest, out chan<- llm.Chunk) (llm.Message, *llm.Usage, error) {
	ch, err := a.Provider.Stream(ctx, req)
	if err != nil {
		return llm.Message{}, nil, err
	}
	msg := llm.Message{Role: llm.RoleAssistant}
	var content strings.Builder
	var usage *llm.Usage

	for chunk := range ch {
		select {
		case out <- chunk:
		case <-ctx.Done():
			return llm.Message{}, nil, ctx.Err()
		}
		if chunk.Delta != "" {
			content.WriteString(chunk.Delta)
		}
		if chunk.ToolCall != nil {
			msg.ToolCalls = append(msg.ToolCalls, *chunk.ToolCall)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	msg.Content = content.String()
	return msg, usage, nil
}

// runTool resolves a ToolCall against a.Tools, invokes the tool, and
// formats the outcome as a tool-role llm.Message ready to append. Unknown
// tools and invocation errors are surfaced through the Content string
// rather than as hard failures — the model typically recovers by choosing
// a different action on the next turn.
func (a *Agent) runTool(ctx context.Context, call llm.ToolCall) llm.Message {
	msg := llm.Message{
		Role:       llm.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
	}
	t := a.findTool(call.Name)
	if t == nil {
		msg.Content = fmt.Sprintf(`{"error":"unknown tool %q"}`, call.Name)
		return msg
	}
	result, err := t.Invoke(ctx, call.Arguments)
	if err != nil {
		msg.Content = fmt.Sprintf(`{"error":%q}`, err.Error())
		return msg
	}
	if len(result) == 0 {
		msg.Content = "{}"
		return msg
	}
	// Keep valid JSON as-is; fall back to quoting non-JSON bytes so the
	// transcript stays machine-readable.
	if json.Valid(result) {
		msg.Content = string(result)
	} else {
		b, _ := json.Marshal(string(result))
		msg.Content = string(b)
	}
	return msg
}

func (a *Agent) findTool(name string) tool.Tool {
	for _, t := range a.Tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// fail sends an Error back to the sender (if any) and returns nil so the
// supervisor does not restart the Agent on a Prompt-level failure.
// Provider outages and budget exhaustion are domain errors, not actor
// crashes.
func (a *Agent) fail(actx actor.Context, p Prompt, err error) error {
	actx.Log().Warn("agent prompt failed", "agent", a.ID, "err", err.Error())
	_ = a.reply(actx, Error{Err: err})
	return nil
}

// reply sends msg through actor.Context.Respond, tolerating the absence of
// a reply destination (fire-and-forget Tell from outside the system).
func (a *Agent) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("agent response dropped", "agent", a.ID, "err", err.Error())
	}
	return nil
}

func accumulateUsage(total *llm.Usage, u *llm.Usage) {
	if u == nil {
		return
	}
	total.PromptTokens += u.PromptTokens
	total.CompletionTokens += u.CompletionTokens
	total.TotalTokens += u.TotalTokens
}

// Spec builds an actor.Spec that spawns a fresh Agent per restart. The
// Agent value captured here is used as the template; Memory, Tools,
// Provider, and Policy.Budget are shared (pointers/interfaces). If the
// caller wants each restart to reset Memory, they must build it inside
// the Factory instead of passing a shared value.
func Spec(name string, template Agent) actor.Spec {
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			cp := template
			return &cp
		},
	}
}

// Compile-time check.
var _ actor.Actor = (*Agent)(nil)
