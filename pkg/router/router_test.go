package router_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/router"
)

func newSys(t *testing.T) *actor.System {
	t.Helper()
	sys := actor.NewSystem("rtest")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	})
	return sys
}

func TestRoundRobinDispatch(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	var hits atomic.Int64
	ref, err := router.Spawn(sys, actor.Spec{
		Name: "rr",
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(actor.Context, any) error {
				hits.Add(1)
				return nil
			})
		},
	}, router.RoundRobin, 4, router.Config{})
	if err != nil {
		t.Fatal(err)
	}

	const N = 100
	for i := 0; i < N; i++ {
		if err := ref.Tell(i); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hits.Load() < int64(N) {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() != int64(N) {
		t.Fatalf("processed %d / %d", hits.Load(), N)
	}
}

func TestBroadcast(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	var hits atomic.Int64
	const workers = 3
	ref, err := router.Spawn(sys, actor.Spec{
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(actor.Context, any) error {
				hits.Add(1)
				return nil
			})
		},
	}, router.Broadcast, workers, router.Config{})
	if err != nil {
		t.Fatal(err)
	}

	_ = ref.Tell("hello")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && hits.Load() < workers {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() != int64(workers) {
		t.Fatalf("broadcast hits = %d, want %d", hits.Load(), workers)
	}
}

type hashMsg struct {
	key string
	val string
}

func (h hashMsg) HashKey() string { return h.key }

func TestConsistentHashStability(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	keyWorker := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	ref, err := router.Spawn(sys, actor.Spec{
		Factory: func() actor.Actor {
			workerID := fmt.Sprintf("%p", &struct{}{}) // unique per instance
			return actor.ActorFunc(func(ctx actor.Context, msg any) error {
				m := msg.(hashMsg)
				mu.Lock()
				prev, seen := keyWorker[m.key]
				if !seen {
					keyWorker[m.key] = workerID
				} else if prev != workerID {
					t.Errorf("key %q hit both %q and %q", m.key, prev, workerID)
				}
				mu.Unlock()
				wg.Done()
				return nil
			})
		},
	}, router.ConsistentHash, 4, router.Config{})
	if err != nil {
		t.Fatal(err)
	}

	keys := []string{"alpha", "beta", "gamma", "delta"}
	const rounds = 5
	wg.Add(len(keys) * rounds)
	for r := 0; r < rounds; r++ {
		for _, k := range keys {
			_ = ref.Tell(hashMsg{key: k, val: fmt.Sprintf("r%d", r)})
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consistent-hash dispatch timed out")
	}
}

func TestLeastLoadedRoutesAroundBacklog(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	var nextID atomic.Int32
	var w0Count, w1Count atomic.Int32
	// Two workers, all messages take 30ms to process so backlogs build up.
	// The first two Tells tie at load=0 — iteration order favours w0 twice,
	// at which point w0.Load=1 while w1.Load=0 and subsequent messages must
	// land on w1.
	ref, err := router.Spawn(sys, actor.Spec{
		Factory: func() actor.Actor {
			id := nextID.Add(1) - 1
			return actor.ActorFunc(func(ctx actor.Context, msg any) error {
				time.Sleep(30 * time.Millisecond)
				if id == 0 {
					w0Count.Add(1)
				} else {
					w1Count.Add(1)
				}
				return nil
			})
		},
	}, router.LeastLoaded, 2, router.Config{})
	if err != nil {
		t.Fatal(err)
	}

	const N = 10
	for i := 0; i < N; i++ {
		_ = ref.Tell(i)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && w0Count.Load()+w1Count.Load() < N {
		time.Sleep(10 * time.Millisecond)
	}
	total := w0Count.Load() + w1Count.Load()
	if total != N {
		t.Fatalf("processed %d/%d", total, N)
	}
	// Distribution should be roughly balanced once backlog kicks in; reject
	// a pathological "everything on one worker" outcome (indicative of a
	// broken LeastLoaded).
	if w1Count.Load() == 0 {
		t.Fatalf("LeastLoaded never picked w1 (w0=%d w1=%d)", w0Count.Load(), w1Count.Load())
	}
}

func TestAskThroughRoundRobin(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	ref, err := router.Spawn(sys, actor.Spec{
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(ctx actor.Context, msg any) error {
				return ctx.Respond(fmt.Sprintf("handled:%v", msg))
			})
		},
	}, router.RoundRobin, 3, router.Config{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := ref.Ask(ctx, "query")
	if err != nil {
		t.Fatalf("Ask through router: %v", err)
	}
	if resp != "handled:query" {
		t.Fatalf("got %v, want handled:query", resp)
	}
}

func TestStoppingRouterCascadesToWorkers(t *testing.T) {
	t.Parallel()
	sys := newSys(t)

	ref, err := router.Spawn(sys, actor.Spec{
		Name: "pool",
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(actor.Context, any) error { return nil })
		},
	}, router.RoundRobin, 3, router.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Send one message to trigger lazy worker init.
	_ = ref.Tell("init")
	time.Sleep(50 * time.Millisecond)

	// Expect 1 router + 3 workers in the registry.
	if n := len(sys.List()); n != 4 {
		t.Fatalf("after init List len = %d, want 4", n)
	}

	_ = ref.Stop()
	// Wait for context cancellation to unwind every runloop.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sys.List()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("after Stop List len = %d, want 0 (survivors: %v)", len(sys.List()), sys.List())
}
