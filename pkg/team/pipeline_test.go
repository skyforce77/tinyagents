package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// transformer is a tiny actor that accepts agent.Prompt, applies fn to the
// text, and responds with agent.Response. Stage errors (fn returning err)
// produce agent.Error instead.
type transformer struct {
	id    string
	fn    func(string) (string, error)
	calls *atomic.Int32
	usage *llm.Usage
}

func (t *transformer) Receive(actx actor.Context, msg any) error {
	if t.calls != nil {
		t.calls.Add(1)
	}
	p, ok := msg.(agent.Prompt)
	if !ok {
		return fmt.Errorf("transformer: unexpected %T", msg)
	}
	if p.Stream != nil {
		defer close(p.Stream)
		p.Stream <- llm.Chunk{Delta: "stream-from-" + t.id}
	}
	out, err := t.fn(p.Text)
	if err != nil {
		return actx.Respond(agent.Error{Err: err})
	}
	return actx.Respond(agent.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Content: out},
		Usage:   t.usage,
	})
}

func spawnTransformer(t *testing.T, sys *actor.System, id string, fn func(string) (string, error), calls *atomic.Int32, usage *llm.Usage) actor.Ref {
	t.Helper()
	ref, err := sys.Spawn(actor.Spec{
		Name: id,
		Factory: func() actor.Actor {
			return &transformer{id: id, fn: fn, calls: calls, usage: usage}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func newTestSystem(t *testing.T) *actor.System {
	t.Helper()
	sys := actor.NewSystem("team-test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	})
	return sys
}

func TestPipelineChainsStages(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	calls := &atomic.Int32{}
	upper := spawnTransformer(t, sys, "upper",
		func(s string) (string, error) { return strings.ToUpper(s), nil },
		calls, &llm.Usage{TotalTokens: 3})
	suffix := spawnTransformer(t, sys, "suffix",
		func(s string) (string, error) { return s + "!", nil },
		calls, &llm.Usage{TotalTokens: 2})

	pipeRef, err := sys.Spawn(Pipeline("p", upper, suffix))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := pipeRef.Ask(ctx, agent.Prompt{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reply.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want Response", reply)
	}
	if r.Message.Content != "HI!" {
		t.Fatalf("content = %q, want HI!", r.Message.Content)
	}
	if r.Usage == nil || r.Usage.TotalTokens != 5 {
		t.Fatalf("aggregated usage = %+v, want TotalTokens=5", r.Usage)
	}
	if calls.Load() != 2 {
		t.Fatalf("stage calls = %d, want 2", calls.Load())
	}
}

func TestPipelineStopsOnStageError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	calls := &atomic.Int32{}
	ok := spawnTransformer(t, sys, "ok",
		func(s string) (string, error) { return s, nil }, calls, nil)
	broken := spawnTransformer(t, sys, "broken",
		func(string) (string, error) { return "", errors.New("kaboom") }, calls, nil)
	later := spawnTransformer(t, sys, "later",
		func(s string) (string, error) { return s, nil }, calls, nil)

	pipeRef, _ := sys.Spawn(Pipeline("p2", ok, broken, later))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := pipeRef.Ask(ctx, agent.Prompt{Text: "in"})
	if err != nil {
		t.Fatal(err)
	}
	e, isErr := reply.(agent.Error)
	if !isErr {
		t.Fatalf("got %T, want Error", reply)
	}
	if !strings.Contains(e.Err.Error(), "stage 1") || !strings.Contains(e.Err.Error(), "kaboom") {
		t.Fatalf("error = %v", e.Err)
	}
	// later stage must not have been called.
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (ok + broken)", calls.Load())
	}
}

func TestPipelineClosesStreamOnEarlyError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	ok := spawnTransformer(t, sys, "ok",
		func(s string) (string, error) { return s, nil }, nil, nil)
	broken := spawnTransformer(t, sys, "broken",
		func(string) (string, error) { return "", errors.New("kaboom") }, nil, nil)
	later := spawnTransformer(t, sys, "later",
		func(s string) (string, error) { return s, nil }, nil, nil)

	pipeRef, _ := sys.Spawn(Pipeline("p3", ok, broken, later))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 4)
	_, err := pipeRef.Ask(ctx, agent.Prompt{Text: "in", Stream: stream})
	if err != nil {
		t.Fatal(err)
	}
	// Stream must be closed even though the final stage never ran.
	select {
	case _, open := <-stream:
		if open {
			// A chunk from the first stage is fine; drain until closed.
			for range stream {
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("stream never closed after early pipeline error")
	}
}

func TestPipelineStreamsOnlyFinalStage(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	first := spawnTransformer(t, sys, "first",
		func(s string) (string, error) { return s + "-1", nil }, nil, nil)
	last := spawnTransformer(t, sys, "last",
		func(s string) (string, error) { return s + "-2", nil }, nil, nil)

	pipeRef, _ := sys.Spawn(Pipeline("p4", first, last))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 4)
	respCh := make(chan any, 1)
	go func() {
		r, _ := pipeRef.Ask(ctx, agent.Prompt{Text: "seed", Stream: stream})
		respCh <- r
	}()

	var parts []string
	for c := range stream {
		if c.Delta != "" {
			parts = append(parts, c.Delta)
		}
	}
	// Only the last stage emits a chunk: "stream-from-last".
	if len(parts) != 1 || parts[0] != "stream-from-last" {
		t.Fatalf("stream deltas = %v, want [stream-from-last]", parts)
	}
	r := (<-respCh).(agent.Response)
	if r.Message.Content != "seed-1-2" {
		t.Fatalf("final content = %q, want seed-1-2", r.Message.Content)
	}
}

func TestPipelinePanicsWithNoStages(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty Pipeline")
		}
	}()
	_ = Pipeline("empty")
}
