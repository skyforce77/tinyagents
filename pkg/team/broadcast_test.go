package team

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm"
)

func TestBroadcastFansOutInOrder(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	// Three members with distinct outputs and distinct usages.
	a := spawnTransformer(t, sys, "a",
		func(s string) (string, error) { return "A:" + s, nil },
		nil, &llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3})
	b := spawnTransformer(t, sys, "b",
		func(s string) (string, error) { return "B:" + s, nil },
		nil, &llm.Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9})
	c := spawnTransformer(t, sys, "c",
		func(s string) (string, error) { return "C:" + s, nil },
		nil, &llm.Usage{PromptTokens: 7, CompletionTokens: 8, TotalTokens: 15})

	bcRef, err := sys.Spawn(Broadcast("bc", a, b, c))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	reply, err := bcRef.Ask(ctx, agent.Prompt{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reply.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", reply)
	}

	want := "A:hello\n---\nB:hello\n---\nC:hello"
	if r.Message.Content != want {
		t.Fatalf("content = %q, want %q", r.Message.Content, want)
	}
	if r.Message.Role != llm.RoleAssistant {
		t.Fatalf("role = %q, want assistant", r.Message.Role)
	}
	if r.Usage == nil {
		t.Fatal("usage is nil, want non-nil")
	}
	if r.Usage.PromptTokens != 12 || r.Usage.CompletionTokens != 15 || r.Usage.TotalTokens != 27 {
		t.Fatalf("usage = %+v, want {12 15 27}", r.Usage)
	}
}

func TestBroadcastErrorShortCircuits(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	kaboom := errors.New("kaboom from member 1")

	a := spawnTransformer(t, sys, "a",
		func(s string) (string, error) { return "A:" + s, nil }, nil, nil)
	b := spawnTransformer(t, sys, "b",
		func(string) (string, error) { return "", kaboom }, nil, nil)
	c := spawnTransformer(t, sys, "c",
		func(s string) (string, error) { return "C:" + s, nil }, nil, nil)

	bcRef, err := sys.Spawn(Broadcast("bc-err", a, b, c))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	reply, err := bcRef.Ask(ctx, agent.Prompt{Text: "in"})
	if err != nil {
		t.Fatal(err)
	}
	e, isErr := reply.(agent.Error)
	if !isErr {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	// Error must mention member index 1.
	if !strings.Contains(e.Err.Error(), "member 1") {
		t.Fatalf("error does not mention member 1: %v", e.Err)
	}
	// Error must wrap the original error.
	if !errors.Is(e.Err, kaboom) {
		t.Fatalf("error does not wrap kaboom: %v", e.Err)
	}
}

func TestBroadcastRejectsStream(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	a := spawnTransformer(t, sys, "a",
		func(s string) (string, error) { return s, nil }, nil, nil)

	bcRef, err := sys.Spawn(Broadcast("bc-stream", a))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 4)
	reply, err := bcRef.Ask(ctx, agent.Prompt{Text: "x", Stream: stream})
	if err != nil {
		t.Fatal(err)
	}
	e, isErr := reply.(agent.Error)
	if !isErr {
		t.Fatalf("got %T, want agent.Error", reply)
	}
	if !strings.Contains(e.Err.Error(), "streaming") {
		t.Fatalf("error does not mention streaming: %v", e.Err)
	}

	// Stream must be closed.
	select {
	case _, open := <-stream:
		if open {
			t.Fatal("stream is open but should be closed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("stream was not closed")
	}
}

func TestBroadcastEmptyPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty Broadcast")
		}
	}()
	_ = Broadcast("empty")
}

func TestBroadcastUsageNilWhenNoneReported(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	// Members that report no usage (nil usage).
	a := spawnTransformer(t, sys, "a",
		func(s string) (string, error) { return "A", nil }, nil, nil)
	b := spawnTransformer(t, sys, "b",
		func(s string) (string, error) { return "B", nil }, nil, nil)

	bcRef, err := sys.Spawn(Broadcast("bc-nil-usage", a, b))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	reply, err := bcRef.Ask(ctx, agent.Prompt{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reply.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", reply)
	}
	if r.Usage != nil {
		t.Fatalf("usage = %+v, want nil", r.Usage)
	}
	if r.Message.Content != "A\n---\nB" {
		t.Fatalf("content = %q", r.Message.Content)
	}
}
