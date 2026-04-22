package team

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

func TestDebateRunsRoundsAndAsksArbiter(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var d0Calls, d1Calls, arbCalls atomic.Int32

	d0 := spawnTransformer(t, sys, "d0",
		func(s string) (string, error) { return "reply-d0", nil },
		&d0Calls, nil)
	d1 := spawnTransformer(t, sys, "d1",
		func(s string) (string, error) { return "reply-d1", nil },
		&d1Calls, nil)
	arb := spawnTransformer(t, sys, "arb",
		func(s string) (string, error) {
			arbCalls.Add(1)
			// Verify transcript contains all expected turn labels.
			want := []string{
				"Debater 0 (turn 1): ",
				"Debater 1 (turn 1): ",
				"Debater 0 (turn 2): ",
				"Debater 1 (turn 2): ",
				"As the arbiter, analyze the debate and declare a verdict.",
			}
			for _, w := range want {
				if !strings.Contains(s, w) {
					t.Errorf("arbiter transcript missing %q", w)
				}
			}
			return "verdict", nil
		},
		nil, nil)

	debateRef, err := sys.Spawn(Debate("debate1", 2, arb, d0, d1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := debateRef.Ask(ctx, agent.Prompt{Text: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reply.(agent.Response); !ok {
		t.Fatalf("got %T, want agent.Response", reply)
	}

	if d0Calls.Load() != 2 {
		t.Errorf("debater 0 calls = %d, want 2", d0Calls.Load())
	}
	if d1Calls.Load() != 2 {
		t.Errorf("debater 1 calls = %d, want 2", d1Calls.Load())
	}
	if arbCalls.Load() != 1 {
		t.Errorf("arbiter calls = %d, want 1", arbCalls.Load())
	}
}

func TestDebateFinalResponseIsArbiterVerdict(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	const verdict = "THE_VERDICT"

	d0 := spawnTransformer(t, sys, "d0",
		func(string) (string, error) { return "x", nil },
		nil, &llm.Usage{TotalTokens: 1})
	d1 := spawnTransformer(t, sys, "d1",
		func(string) (string, error) { return "y", nil },
		nil, &llm.Usage{TotalTokens: 2})
	arb := spawnTransformer(t, sys, "arb",
		func(string) (string, error) { return verdict, nil },
		nil, &llm.Usage{TotalTokens: 10})

	debateRef, err := sys.Spawn(Debate("debate2", 1, arb, d0, d1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := debateRef.Ask(ctx, agent.Prompt{Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reply.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", reply)
	}
	if r.Message.Content != verdict {
		t.Errorf("content = %q, want %q", r.Message.Content, verdict)
	}
	if r.Usage == nil {
		t.Fatal("usage is nil, want non-nil")
	}
	// Arbiter contributes 10; debaters 1+2 = 3; total must be at least arbiter's 10.
	if r.Usage.TotalTokens < 10 {
		t.Errorf("total tokens = %d, want >= 10", r.Usage.TotalTokens)
	}
}

func TestDebateDebaterErrorShortCircuits(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var arbCalls atomic.Int32

	// debater 0 always succeeds.
	d0 := spawnTransformer(t, sys, "d0",
		func(string) (string, error) { return "ok", nil },
		nil, nil)

	// debater 1 succeeds on turn 1 but fails on turn 2.
	// With 2 rounds: d0 t1 ok, d1 t1 ok, d0 t2 ok, d1 t2 fail.
	var d1Calls atomic.Int32
	d1 := spawnTransformer(t, sys, "d1",
		func(string) (string, error) {
			n := d1Calls.Add(1)
			if n >= 2 {
				return "", errors.New("d1-fail-turn2")
			}
			return "ok", nil
		},
		nil, nil)

	arb := spawnTransformer(t, sys, "arb",
		func(string) (string, error) {
			arbCalls.Add(1)
			return "verdict", nil
		},
		nil, nil)

	debateRef, err := sys.Spawn(Debate("debate3", 2, arb, d0, d1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := debateRef.Ask(ctx, agent.Prompt{Text: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reply.(agent.Error)
	if !ok {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	if !strings.Contains(e.Err.Error(), "debater 1") {
		t.Errorf("error %q does not mention 'debater 1'", e.Err.Error())
	}
	if !strings.Contains(e.Err.Error(), "turn 2") {
		t.Errorf("error %q does not mention 'turn 2'", e.Err.Error())
	}
	if arbCalls.Load() != 0 {
		t.Errorf("arbiter was called %d times, want 0", arbCalls.Load())
	}
}

func TestDebateArbiterErrorPropagated(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	d0 := spawnTransformer(t, sys, "d0",
		func(string) (string, error) { return "ok", nil },
		nil, nil)
	d1 := spawnTransformer(t, sys, "d1",
		func(string) (string, error) { return "ok", nil },
		nil, nil)
	arb := spawnTransformer(t, sys, "arb",
		func(string) (string, error) { return "", errors.New("arb-fail") },
		nil, nil)

	debateRef, err := sys.Spawn(Debate("debate4", 1, arb, d0, d1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply, err := debateRef.Ask(ctx, agent.Prompt{Text: "topic"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reply.(agent.Error)
	if !ok {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	if !strings.Contains(e.Err.Error(), "arbiter") {
		t.Errorf("error %q does not mention 'arbiter'", e.Err.Error())
	}
}

func TestDebateStreamOnlyArbiter(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	// Debaters return normally; transformer writes "stream-from-<id>" when streaming.
	// But Debate never passes Stream to debaters (nil), so no chunk from them.
	d0 := spawnTransformer(t, sys, "d0",
		func(string) (string, error) { return "d0-reply", nil },
		nil, nil)
	d1 := spawnTransformer(t, sys, "d1",
		func(string) (string, error) { return "d1-reply", nil },
		nil, nil)
	arb := spawnTransformer(t, sys, "arb",
		func(string) (string, error) { return "arb-verdict", nil },
		nil, nil)

	debateRef, err := sys.Spawn(Debate("debate5", 1, arb, d0, d1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 8)
	respCh := make(chan any, 1)
	go func() {
		r, _ := debateRef.Ask(ctx, agent.Prompt{Text: "topic", Stream: stream})
		respCh <- r
	}()

	var deltas []string
	for c := range stream {
		if c.Delta != "" {
			deltas = append(deltas, c.Delta)
		}
	}

	// Only the arbiter's delta should be present.
	if len(deltas) != 1 || deltas[0] != "stream-from-arb" {
		t.Errorf("stream deltas = %v, want [stream-from-arb]", deltas)
	}
	r := <-respCh
	resp, ok := r.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", r)
	}
	if resp.Message.Content != "arb-verdict" {
		t.Errorf("content = %q, want arb-verdict", resp.Message.Content)
	}
}

func TestDebateInvalidArgsPanics(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	d0 := spawnTransformer(t, sys, "pd0",
		func(string) (string, error) { return "x", nil },
		nil, nil)
	d1 := spawnTransformer(t, sys, "pd1",
		func(string) (string, error) { return "x", nil },
		nil, nil)
	arb := spawnTransformer(t, sys, "parb",
		func(string) (string, error) { return "x", nil },
		nil, nil)

	t.Run("nil arbiter", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic for nil arbiter")
			}
		}()
		_ = Debate("x", 1, nil, d0, d1)
	})

	t.Run("fewer than 2 debaters", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic for fewer than 2 debaters")
			}
		}()
		_ = Debate("x", 1, arb, d0)
	})

	t.Run("rounds less than 1", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic for rounds < 1")
			}
		}()
		_ = Debate("x", 0, arb, d0, d1)
	})
}
