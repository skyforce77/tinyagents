package cluster_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/cluster"
)

// drain reads up to n events from ch within timeout; returns what it got.
func drain(ch <-chan cluster.Event, n int, timeout time.Duration) []cluster.Event {
	var out []cluster.Event
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

func collectN(t *testing.T, c cluster.Cluster, n int) <-chan cluster.Event {
	t.Helper()
	ch := make(chan cluster.Event, n+4)
	c.Subscribe(func(ev cluster.Event) {
		ch <- ev
	})
	return ch
}

// TestLocalJoinFiresJoinedEvent verifies that a single MemberJoined event
// is delivered to a subscriber after Join.
func TestLocalJoinFiresJoinedEvent(t *testing.T) {
	c := cluster.New("node-1")
	ch := collectN(t, c, 1)

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}

	events := drain(ch, 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Kind != cluster.MemberJoined {
		t.Fatalf("expected MemberJoined, got %v", events[0].Kind)
	}
	if events[0].Member.ID != "node-1" {
		t.Fatalf("expected member id node-1, got %q", events[0].Member.ID)
	}
}

// TestLocalLeaveFiresLeftEvent verifies MemberLeft is delivered after Leave.
func TestLocalLeaveFiresLeftEvent(t *testing.T) {
	c := cluster.New("node-2")
	ch := collectN(t, c, 2)

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := c.Leave(context.Background()); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	events := drain(ch, 2, time.Second)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Kind != cluster.MemberLeft {
		t.Fatalf("expected MemberLeft, got %v", events[1].Kind)
	}
	if events[1].Member.ID != "node-2" {
		t.Fatalf("expected member id node-2, got %q", events[1].Member.ID)
	}
}

// TestLocalMembersAfterJoin verifies Members returns a one-element slice
// containing the local node after a successful Join.
func TestLocalMembersAfterJoin(t *testing.T) {
	c := cluster.New("node-3", cluster.WithAddress("127.0.0.1:7946"))

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}

	members := c.Members()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].ID != "node-3" {
		t.Fatalf("expected member id node-3, got %q", members[0].ID)
	}
	if members[0].Address != "127.0.0.1:7946" {
		t.Fatalf("expected address 127.0.0.1:7946, got %q", members[0].Address)
	}
}

// TestLocalMultipleSubscribers verifies that two independent subscribers
// both receive the same events.
func TestLocalMultipleSubscribers(t *testing.T) {
	c := cluster.New("node-4")

	ch1 := collectN(t, c, 1)
	ch2 := collectN(t, c, 1)

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}

	e1 := drain(ch1, 1, time.Second)
	e2 := drain(ch2, 1, time.Second)

	if len(e1) != 1 || e1[0].Kind != cluster.MemberJoined {
		t.Fatalf("subscriber 1: expected MemberJoined, got %v", e1)
	}
	if len(e2) != 1 || e2[0].Kind != cluster.MemberJoined {
		t.Fatalf("subscriber 2: expected MemberJoined, got %v", e2)
	}
}

// TestLocalUnsubscribeStopsDelivery verifies that the unsubscribe function
// prevents future events from reaching the handler.
func TestLocalUnsubscribeStopsDelivery(t *testing.T) {
	c := cluster.New("node-5")

	var count atomic.Int64
	unsub := c.Subscribe(func(ev cluster.Event) {
		count.Add(1)
	})

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}
	// Allow the goroutine time to deliver the joined event.
	time.Sleep(50 * time.Millisecond)
	joined := count.Load()
	if joined != 1 {
		t.Fatalf("expected 1 event before unsub, got %d", joined)
	}

	unsub()

	if err := c.Leave(context.Background()); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatalf("expected no new events after unsub, got %d total", count.Load())
	}
}

// TestLocalCloseCleansUp verifies that after Close(), Join and Leave return
// errors and subsequent calls are rejected cleanly without panic.
func TestLocalCloseCleansUp(t *testing.T) {
	c := cluster.New("node-6")

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join before Close: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Join after Close must return an error (not panic).
	if err := c.Join(context.Background()); err == nil {
		t.Fatal("expected error from Join after Close, got nil")
	}

	// Leave after Close must return an error (not panic).
	if err := c.Leave(context.Background()); err == nil {
		t.Fatal("expected error from Leave after Close, got nil")
	}

	// Close a second time is a no-op.
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestLocalIdempotentJoin verifies that calling Join twice fires MemberJoined
// exactly once.
func TestLocalIdempotentJoin(t *testing.T) {
	c := cluster.New("node-7")

	var count atomic.Int64
	c.Subscribe(func(ev cluster.Event) {
		if ev.Kind == cluster.MemberJoined {
			count.Add(1)
		}
	})

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("first Join: %v", err)
	}
	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("second Join: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if n := count.Load(); n != 1 {
		t.Fatalf("expected MemberJoined fired once, got %d", n)
	}
}

// TestLocalLeaveBeforeJoin verifies that Leave before Join is a no-op and
// does not panic or fire any events.
func TestLocalLeaveBeforeJoin(t *testing.T) {
	c := cluster.New("node-8")

	var count atomic.Int64
	c.Subscribe(func(_ cluster.Event) { count.Add(1) })

	if err := c.Leave(context.Background()); err != nil {
		t.Fatalf("Leave before Join: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if n := count.Load(); n != 0 {
		t.Fatalf("expected no events, got %d", n)
	}
}

// TestLocalConcurrentSubscribe spawns 100 goroutines that each subscribe and
// immediately unsubscribe, exercising concurrent access to the subscriber map.
// The test is expected to be run with -race.
func TestLocalConcurrentSubscribe(t *testing.T) {
	c := cluster.New("node-9")

	if err := c.Join(context.Background()); err != nil {
		t.Fatalf("Join: %v", err)
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			unsub := c.Subscribe(func(_ cluster.Event) {})
			// Yield to increase interleaving before unsubscribing.
			runtime.Gosched()
			unsub()
		}()
	}

	wg.Wait()

	if err := c.Leave(context.Background()); err != nil {
		t.Fatalf("Leave: %v", err)
	}
}
