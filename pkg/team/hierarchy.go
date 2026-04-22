package team

import (
	"fmt"
	"strings"
	"sync"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Hierarchy builds an actor.Spec for a coordinator that fans out a Prompt to
// every worker in parallel, then passes a synthesis prompt to the lead agent.
//
// Workers run non-streaming. The caller's Prompt.Stream is forwarded only to
// the lead agent, which closes it in its handlePrompt defer. If any worker
// returns agent.Error the coordinator short-circuits, closes the stream (if
// any), and replies with agent.Error wrapping the failure (including the
// worker index). The final Response.Usage is the sum of all worker usages plus
// the lead's usage.
//
// Hierarchy panics if lead is nil or workers is empty.
func Hierarchy(name string, lead actor.Ref, workers ...actor.Ref) actor.Spec {
	if lead == nil {
		panic("team.Hierarchy: lead must not be nil")
	}
	if len(workers) == 0 {
		panic("team.Hierarchy: at least one worker is required")
	}
	wCopy := append([]actor.Ref(nil), workers...)
	return actor.Spec{
		Name: name,
		Factory: func() actor.Actor {
			return &hierarchyActor{lead: lead, workers: wCopy}
		},
	}
}

type hierarchyActor struct {
	lead    actor.Ref
	workers []actor.Ref
}

// workerResult holds the outcome of one worker Ask.
type workerResult struct {
	idx  int
	resp agent.Response
	err  error // non-nil means the worker returned agent.Error or transport err
}

func (h *hierarchyActor) Receive(actx actor.Context, msg any) error {
	prompt, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("team: Hierarchy expects agent.Prompt, got %T", msg)
	}

	// If workers fail we close the stream here; if we reach the lead we do NOT
	// close it — the lead's handlePrompt does that in its defer.
	streamHandedOff := false
	defer func() {
		if prompt.Stream != nil && !streamHandedOff {
			close(prompt.Stream)
		}
	}()

	// --- Phase 1: fan out to all workers in parallel ---
	ctx := actx // actor.Context embeds context.Context
	n := len(h.workers)
	results := make([]workerResult, n)

	var (
		mu       sync.Mutex
		firstErr *workerResult
		wg       sync.WaitGroup
	)
	wg.Add(n)
	for i, w := range h.workers {
		i, w := i, w
		go func() {
			defer wg.Done()
			reply, err := w.Ask(ctx, agent.Prompt{Text: prompt.Text})
			r := workerResult{idx: i}
			if err != nil {
				r.err = fmt.Errorf("worker %d: ask failed: %w", i, err)
			} else {
				switch v := reply.(type) {
				case agent.Response:
					r.resp = v
				case agent.Error:
					r.err = fmt.Errorf("worker %d: %w", i, v.Err)
				default:
					r.err = fmt.Errorf("worker %d: unexpected reply %T", i, reply)
				}
			}
			mu.Lock()
			results[i] = r
			if r.err != nil && firstErr == nil {
				cp := r
				firstErr = &cp
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if firstErr != nil {
		actx.Log().Warn("team hierarchy worker failed", "err", firstErr.err.Error())
		return h.respondErr(actx, fmt.Errorf("team: hierarchy worker error: %w", firstErr.err))
	}

	// --- Phase 2: build synthesis prompt for the lead ---
	var sb strings.Builder
	sb.WriteString("Original question: ")
	sb.WriteString(prompt.Text)
	sb.WriteString("\n")
	for i, r := range results {
		fmt.Fprintf(&sb, "\nWorker %d said:\n%s\n", i, r.resp.Message.Content)
	}
	sb.WriteString("\nSynthesize a single coherent answer combining the workers' perspectives.")

	total := llm.Usage{}
	for _, r := range results {
		if r.resp.Usage != nil {
			total.PromptTokens += r.resp.Usage.PromptTokens
			total.CompletionTokens += r.resp.Usage.CompletionTokens
			total.TotalTokens += r.resp.Usage.TotalTokens
		}
	}

	// --- Phase 3: ask the lead ---
	leadPrompt := agent.Prompt{
		Text:   sb.String(),
		Role:   llm.RoleUser,
		Stream: prompt.Stream,
	}
	streamHandedOff = prompt.Stream != nil

	leadReply, err := h.lead.Ask(actx, leadPrompt)
	if err != nil {
		streamHandedOff = false // lead never owned it — close in defer
		actx.Log().Warn("team hierarchy lead ask failed", "err", err.Error())
		return h.respondErr(actx, fmt.Errorf("team: hierarchy lead ask: %w", err))
	}

	switch r := leadReply.(type) {
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
		return h.reply(actx, agent.Response{Message: r.Message, Usage: usagePtr})
	case agent.Error:
		streamHandedOff = false
		actx.Log().Warn("team hierarchy lead returned error", "err", r.Err.Error())
		return h.respondErr(actx, fmt.Errorf("team: hierarchy lead: %w", r.Err))
	default:
		streamHandedOff = false
		return h.respondErr(actx, fmt.Errorf("team: hierarchy lead unexpected reply %T", leadReply))
	}
}

func (h *hierarchyActor) respondErr(actx actor.Context, err error) error {
	return h.reply(actx, agent.Error{Err: err})
}

func (h *hierarchyActor) reply(actx actor.Context, msg any) error {
	if err := actx.Respond(msg); err != nil {
		actx.Log().Debug("team hierarchy response dropped", "err", err.Error())
	}
	return nil
}
