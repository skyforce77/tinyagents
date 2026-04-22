package team

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// TestHierarchyFansOutToWorkersAndSynthesizes verifies the full happy path:
// two workers transform the input in different ways, the lead receives a
// synthesis prompt that contains both labelled outputs, and the final
// Response carries the lead's content with usage summed across all three.
func TestHierarchyFansOutToWorkersAndSynthesizes(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	workerCalls := &atomic.Int32{}
	leadCalls := &atomic.Int32{}

	upper := spawnTransformer(t, sys, "upper",
		func(s string) (string, error) { return strings.ToUpper(s), nil },
		workerCalls, &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})
	lower := spawnTransformer(t, sys, "lower",
		func(s string) (string, error) { return strings.ToLower(s), nil },
		workerCalls, &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})

	var capturedSynthesis string
	lead, err := sys.Spawn(actor.Spec{
		Name: "lead",
		Factory: func() actor.Actor {
			return &transformer{
				id:    "lead",
				calls: leadCalls,
				usage: &llm.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4},
				fn: func(s string) (string, error) {
					capturedSynthesis = s
					return "synthesized", nil
				},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	hierRef, err := sys.Spawn(Hierarchy("h", lead, upper, lower))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := hierRef.Ask(ctx, agent.Prompt{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	r, ok := reply.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", reply)
	}
	if r.Message.Content != "synthesized" {
		t.Fatalf("content = %q, want synthesized", r.Message.Content)
	}

	// Synthesis prompt must contain both worker outputs labelled by index.
	if !strings.Contains(capturedSynthesis, "Worker 0 said:") {
		t.Errorf("synthesis missing 'Worker 0 said:', got:\n%s", capturedSynthesis)
	}
	if !strings.Contains(capturedSynthesis, "Worker 1 said:") {
		t.Errorf("synthesis missing 'Worker 1 said:', got:\n%s", capturedSynthesis)
	}
	// Workers run with the original prompt; one uppercases, one lowercases.
	if !strings.Contains(capturedSynthesis, "HELLO") {
		t.Errorf("synthesis missing HELLO, got:\n%s", capturedSynthesis)
	}
	if !strings.Contains(capturedSynthesis, "hello") {
		t.Errorf("synthesis missing hello, got:\n%s", capturedSynthesis)
	}
	if !strings.Contains(capturedSynthesis, "Original question: hello") {
		t.Errorf("synthesis missing original question, got:\n%s", capturedSynthesis)
	}

	// Usage: 2 workers x 2 tokens each + lead 4 tokens = 8 total.
	if r.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if r.Usage.TotalTokens != 8 {
		t.Fatalf("TotalTokens = %d, want 8", r.Usage.TotalTokens)
	}

	if workerCalls.Load() != 2 {
		t.Fatalf("worker calls = %d, want 2", workerCalls.Load())
	}
	if leadCalls.Load() != 1 {
		t.Fatalf("lead calls = %d, want 1", leadCalls.Load())
	}
}

// TestHierarchyWorkerErrorShortCircuits checks that when a worker returns an
// error, the coordinator replies with agent.Error mentioning the worker index
// and does NOT invoke the lead.
func TestHierarchyWorkerErrorShortCircuits(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	leadCalls := &atomic.Int32{}

	ok := spawnTransformer(t, sys, "ok0",
		func(s string) (string, error) { return s, nil }, nil, nil)
	broken := spawnTransformer(t, sys, "broken1",
		func(string) (string, error) { return "", errors.New("worker1 kaboom") }, nil, nil)

	lead, err := sys.Spawn(actor.Spec{
		Name: "lead",
		Factory: func() actor.Actor {
			return &transformer{id: "lead", calls: leadCalls,
				fn: func(s string) (string, error) { return s, nil }}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	hierRef, err := sys.Spawn(Hierarchy("h-err", lead, ok, broken))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := hierRef.Ask(ctx, agent.Prompt{Text: "test"})
	if err != nil {
		t.Fatal(err)
	}

	e, isErr := reply.(agent.Error)
	if !isErr {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	errStr := e.Err.Error()
	if !strings.Contains(errStr, "worker 1") {
		t.Errorf("error should mention worker 1, got: %v", errStr)
	}
	if !strings.Contains(errStr, "worker1 kaboom") {
		t.Errorf("error should mention worker1 kaboom, got: %v", errStr)
	}
	if leadCalls.Load() != 0 {
		t.Fatalf("lead should not have been called, got %d calls", leadCalls.Load())
	}
}

// TestHierarchyLeadErrorPropagated verifies that when workers succeed but the
// lead returns agent.Error, that error is forwarded to the caller.
func TestHierarchyLeadErrorPropagated(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	w := spawnTransformer(t, sys, "w",
		func(s string) (string, error) { return s + "-ok", nil }, nil, nil)

	lead, err := sys.Spawn(actor.Spec{
		Name: "lead",
		Factory: func() actor.Actor {
			return &transformer{id: "lead",
				fn: func(string) (string, error) { return "", errors.New("lead blew up") }}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	hierRef, err := sys.Spawn(Hierarchy("h-lead-err", lead, w))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := hierRef.Ask(ctx, agent.Prompt{Text: "ping"})
	if err != nil {
		t.Fatal(err)
	}

	e, isErr := reply.(agent.Error)
	if !isErr {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	if !strings.Contains(e.Err.Error(), "lead blew up") {
		t.Fatalf("expected 'lead blew up' in error, got: %v", e.Err)
	}
}

// TestHierarchyNilLeadPanics verifies that passing nil as the lead panics.
func TestHierarchyNilLeadPanics(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)
	w := spawnTransformer(t, sys, "wnil",
		func(s string) (string, error) { return s, nil }, nil, nil)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil lead")
		}
	}()
	_ = Hierarchy("nil-lead", nil, w)
}

// TestHierarchyEmptyWorkersPanics verifies that passing no workers panics.
func TestHierarchyEmptyWorkersPanics(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)
	lead, err := sys.Spawn(actor.Spec{
		Name: "lead-empty",
		Factory: func() actor.Actor {
			return &transformer{fn: func(s string) (string, error) { return s, nil }}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty workers")
		}
	}()
	_ = Hierarchy("no-workers", lead)
}

// TestHierarchyClosesStreamOnWorkerError verifies that when a worker errors
// before the lead runs, the caller's stream channel is closed so a ranging
// goroutine unblocks.
func TestHierarchyClosesStreamOnWorkerError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	broken := spawnTransformer(t, sys, "broken-stream",
		func(string) (string, error) { return "", errors.New("stream-worker-fail") }, nil, nil)

	lead, err := sys.Spawn(actor.Spec{
		Name: "lead-stream",
		Factory: func() actor.Actor {
			return &transformer{fn: func(s string) (string, error) { return s, nil }}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	hierRef, err := sys.Spawn(Hierarchy("h-stream-err", lead, broken))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 4)
	_, err = hierRef.Ask(ctx, agent.Prompt{Text: "in", Stream: stream})
	if err != nil {
		t.Fatal(err)
	}

	// Drain stream; it must be closed since the lead never ran.
	select {
	case _, open := <-stream:
		if open {
			for range stream {
			}
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("stream was not closed after worker error")
	}
}
