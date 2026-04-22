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

// textKeyFn is a KeyFunc that returns the prompt text as the routing key.
func textKeyFn(p agent.Prompt) string { return p.Text }

// findKeysForIndices returns a slice of n keys such that keys[i] hashes to
// index i (with n members). It iterates until it finds one per bucket.
func findKeysForIndices(n int) []string {
	keys := make([]string, n)
	found := make([]bool, n)
	remaining := n
	for i := 0; remaining > 0; i++ {
		k := fmt.Sprintf("route-key-%d", i)
		idx := routerIndex(k, n)
		if !found[idx] {
			keys[idx] = k
			found[idx] = true
			remaining--
		}
	}
	return keys
}

// TestRouterDispatchesByKey verifies that distinct keys route to distinct
// members when the keys are chosen to cover all indices.
func TestRouterDispatchesByKey(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	const nMembers = 3
	counters := make([]*atomic.Int32, nMembers)
	refs := make([]actor.Ref, nMembers)
	for i := range nMembers {
		c := &atomic.Int32{}
		counters[i] = c
		idx := i // capture
		refs[i] = spawnTransformer(t, sys, fmt.Sprintf("m%d", i),
			func(s string) (string, error) { return s + fmt.Sprintf("-from-%d", idx), nil },
			c, nil)
	}

	routerRef, err := sys.Spawn(Router("r", textKeyFn, refs...))
	if err != nil {
		t.Fatal(err)
	}

	// Find one key per index so each member is called exactly once.
	keys := findKeysForIndices(nMembers)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, k := range keys {
		_, err := routerRef.Ask(ctx, agent.Prompt{Text: k})
		if err != nil {
			t.Fatalf("Ask(%q) err: %v", k, err)
		}
	}

	for i, c := range counters {
		if c.Load() != 1 {
			t.Errorf("member %d call count = %d, want 1", i, c.Load())
		}
	}
}

// TestRouterStableMapping confirms that the same key always routes to the
// same member across multiple calls.
func TestRouterStableMapping(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	const nMembers = 5
	counters := make([]*atomic.Int32, nMembers)
	refs := make([]actor.Ref, nMembers)
	for i := range nMembers {
		c := &atomic.Int32{}
		counters[i] = c
		refs[i] = spawnTransformer(t, sys, fmt.Sprintf("sm%d", i),
			func(s string) (string, error) { return s, nil },
			c, nil)
	}

	routerRef, err := sys.Spawn(Router("r-stable", textKeyFn, refs...))
	if err != nil {
		t.Fatal(err)
	}

	const key = "always-same"
	expectedIdx := routerIndex(key, nMembers)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	const sends = 5
	for range sends {
		_, err := routerRef.Ask(ctx, agent.Prompt{Text: key})
		if err != nil {
			t.Fatal(err)
		}
	}

	if got := counters[expectedIdx].Load(); got != sends {
		t.Errorf("member %d count = %d, want %d", expectedIdx, got, sends)
	}
	for i, c := range counters {
		if i != expectedIdx && c.Load() != 0 {
			t.Errorf("member %d should not have been called, got %d", i, c.Load())
		}
	}
}

// TestRouterRelaysResponse confirms the chosen member's Response (including
// Usage) is relayed verbatim to the caller.
func TestRouterRelaysResponse(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	want := &llm.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
	m := spawnTransformer(t, sys, "relay-m",
		func(s string) (string, error) { return "echo:" + s, nil },
		nil, want)

	routerRef, err := sys.Spawn(Router("r-relay", textKeyFn, m))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	raw, err := routerRef.Ask(ctx, agent.Prompt{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := raw.(agent.Response)
	if !ok {
		t.Fatalf("got %T, want agent.Response", raw)
	}
	if resp.Message.Content != "echo:hello" {
		t.Errorf("content = %q, want echo:hello", resp.Message.Content)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil, want non-nil")
	}
	if *resp.Usage != *want {
		t.Errorf("usage = %+v, want %+v", *resp.Usage, *want)
	}
}

// TestRouterPropagatesError confirms that when the chosen member returns an
// agent.Error, the Router wraps it noting the member index and relays it.
func TestRouterPropagatesError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	m := spawnTransformer(t, sys, "err-m",
		func(string) (string, error) { return "", errors.New("member-exploded") },
		nil, nil)

	routerRef, err := sys.Spawn(Router("r-err", textKeyFn, m))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	raw, err := routerRef.Ask(ctx, agent.Prompt{Text: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	ae, ok := raw.(agent.Error)
	if !ok {
		t.Fatalf("got %T, want agent.Error", raw)
	}
	// Error message must mention both the member index and the cause.
	if !strings.Contains(ae.Err.Error(), "member 0") {
		t.Errorf("error %q does not mention member index", ae.Err.Error())
	}
	if !strings.Contains(ae.Err.Error(), "member-exploded") {
		t.Errorf("error %q does not mention original cause", ae.Err.Error())
	}
}

// TestRouterEmptyKeyRoutesToMemberZero verifies the documented behaviour:
// an empty key always routes to member 0.
func TestRouterEmptyKeyRoutesToMemberZero(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	counters := [2]*atomic.Int32{{}, {}}
	refs := make([]actor.Ref, 2)
	for i := range 2 {
		c := counters[i]
		refs[i] = spawnTransformer(t, sys, fmt.Sprintf("ez%d", i),
			func(s string) (string, error) { return s, nil }, c, nil)
	}

	emptyKeyFn := func(agent.Prompt) string { return "" }
	routerRef, err := sys.Spawn(Router("r-empty", emptyKeyFn, refs...))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for range 3 {
		_, err := routerRef.Ask(ctx, agent.Prompt{Text: "whatever"})
		if err != nil {
			t.Fatal(err)
		}
	}

	if counters[0].Load() != 3 {
		t.Errorf("member 0 calls = %d, want 3", counters[0].Load())
	}
	if counters[1].Load() != 0 {
		t.Errorf("member 1 calls = %d, want 0", counters[1].Load())
	}
}

// TestRouterClosesStreamOnAskError verifies that when Ask fails because the
// member actor has been stopped, the Router closes the stream so the caller
// is not left blocking on a channel that will never be drained.
func TestRouterClosesStreamOnAskError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	// Spawn a member, then stop it so any subsequent Ask returns an error.
	m := spawnTransformer(t, sys, "dead-member",
		func(s string) (string, error) { return s, nil }, nil, nil)
	_ = m.Stop()
	// Give the stop a moment to propagate so the mailbox is closed.
	time.Sleep(20 * time.Millisecond)

	routerRef, err := sys.Spawn(Router("r-stream-close", textKeyFn, m))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream := make(chan llm.Chunk, 4)
	_, _ = routerRef.Ask(ctx, agent.Prompt{Text: "test", Stream: stream})

	// The stream must be closed by the Router on Ask error.
	select {
	case _, open := <-stream:
		if open {
			for range stream {
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream was not closed after member Ask error")
	}
}

func TestRouterNilKeyFnPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil keyFn")
		}
	}()
	sys := newTestSystem(t)
	m := spawnTransformer(t, sys, "m", func(s string) (string, error) { return s, nil }, nil, nil)
	_ = Router("r", nil, m)
}

func TestRouterEmptyMembersPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty members")
		}
	}()
	_ = Router("r", textKeyFn)
}
