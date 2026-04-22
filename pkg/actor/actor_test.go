package actor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/supervisor"
)

func newTestSystem(t *testing.T) *System {
	t.Helper()
	sys := NewSystem("test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	})
	return sys
}

func TestSpawnTellReceive(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	received := make(chan any, 1)
	ref, err := sys.Spawn(Spec{
		Name: "echo",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				received <- msg
				return nil
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := ref.Tell("hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-received:
		if m != "hello" {
			t.Fatalf("got %v, want hello", m)
		}
	case <-time.After(time.Second):
		t.Fatal("message not received")
	}
}

func TestAskRespond(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	ref, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				if s, ok := msg.(string); ok {
					return ctx.Respond("echo:" + s)
				}
				return nil
			})
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := ref.Ask(ctx, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if resp != "echo:hi" {
		t.Fatalf("got %v, want echo:hi", resp)
	}
}

func TestAskTimeout(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	ref, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				// Never respond.
				return nil
			})
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ref.Ask(ctx, "hi")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}

func TestSenderViaContextTell(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var gotSenderPID PID
	var wg sync.WaitGroup
	wg.Add(1)

	// Receiver reports the sender PID observed in Context.
	receiver, _ := sys.Spawn(Spec{
		Name: "receiver",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				if s := ctx.Sender(); s != nil {
					gotSenderPID = s.PID()
				}
				wg.Done()
				return nil
			})
		},
	})

	// Sender actor forwards an external message to receiver using ctx.Tell.
	sender, _ := sys.Spawn(Spec{
		Name: "sender",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				return ctx.Tell(receiver, msg)
			})
		},
	})
	_ = sender.Tell("payload")

	wg.Wait()
	if gotSenderPID.Path != "/user/sender" {
		t.Fatalf("sender path = %q, want /user/sender", gotSenderPID.Path)
	}
}

func TestForwardPreservesOriginalSender(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var gotSenderPID PID
	done := make(chan struct{})

	final, _ := sys.Spawn(Spec{
		Name: "final",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				if s := ctx.Sender(); s != nil {
					gotSenderPID = s.PID()
				}
				close(done)
				return nil
			})
		},
	})
	middle, _ := sys.Spawn(Spec{
		Name: "middle",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				return ctx.Forward(final, msg) // preserves original sender
			})
		},
	})
	originator, _ := sys.Spawn(Spec{
		Name: "originator",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				return ctx.Tell(middle, msg)
			})
		},
	})
	_ = originator.Tell("payload")

	<-done
	if gotSenderPID.Path != "/user/originator" {
		t.Fatalf("sender path = %q, want /user/originator", gotSenderPID.Path)
	}
}

func TestLocalStoreIsolation(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	done := make(chan int, 2)
	factory := func() Actor {
		return ActorFunc(func(ctx Context, msg any) error {
			n, _ := ctx.Store().Get("count")
			count := 0
			if v, ok := n.(int); ok {
				count = v
			}
			count++
			ctx.Store().Set("count", count)
			done <- count
			return nil
		})
	}

	a, _ := sys.Spawn(Spec{Name: "a", Factory: factory})
	b, _ := sys.Spawn(Spec{Name: "b", Factory: factory})

	_ = a.Tell(nil)
	_ = a.Tell(nil)
	_ = b.Tell(nil)

	got := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		got = append(got, <-done)
	}
	// a should have counted to 2, b to 1. Order of two `a` messages is
	// guaranteed; order of b relative to a is not.
	countsBySource := map[int]int{}
	for _, v := range got {
		countsBySource[v]++
	}
	// Two 1s (one from a's first msg, one from b) and one 2 (a's second).
	if countsBySource[1] != 2 || countsBySource[2] != 1 {
		t.Fatalf("unexpected counts %v", countsBySource)
	}
}

func TestGlobalStoreIsShared(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	done := make(chan struct{}, 2)
	writer, _ := sys.Spawn(Spec{
		Name: "writer",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				ctx.Global().Set("k", "v")
				done <- struct{}{}
				return nil
			})
		},
	})
	_ = writer.Tell(nil)
	<-done

	reader, _ := sys.Spawn(Spec{
		Name: "reader",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				v, ok := ctx.Global().Get("k")
				if !ok || v != "v" {
					t.Errorf("reader got %v, ok=%v", v, ok)
				}
				done <- struct{}{}
				return nil
			})
		},
	})
	_ = reader.Tell(nil)
	<-done
}

func TestChildSpawn(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	childPIDCh := make(chan PID, 1)
	parent, _ := sys.Spawn(Spec{
		Name: "parent",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				child, err := ctx.Spawn(Spec{
					Name: "kid",
					Factory: func() Actor {
						return ActorFunc(func(Context, any) error { return nil })
					},
				})
				if err != nil {
					return err
				}
				childPIDCh <- child.PID()
				return nil
			})
		},
	})
	_ = parent.Tell(nil)

	select {
	case pid := <-childPIDCh:
		if pid.Path != "/user/parent/kid" {
			t.Fatalf("child path = %q, want /user/parent/kid", pid.Path)
		}
	case <-time.After(time.Second):
		t.Fatal("child not spawned")
	}
}

func TestSupervisorRestartsOnPanic(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var starts atomic.Int32
	var panicked atomic.Bool
	done := make(chan struct{}, 1)

	ref, _ := sys.Spawn(Spec{
		Name: "flaky",
		Factory: func() Actor {
			starts.Add(1)
			return ActorFunc(func(ctx Context, msg any) error {
				if !panicked.Load() {
					panicked.Store(true)
					panic("boom")
				}
				done <- struct{}{}
				return nil
			})
		},
		Supervisor: supervisor.NewRestart(5, time.Minute, time.Millisecond, time.Millisecond),
	})

	_ = ref.Tell("first")  // panics, triggers restart
	_ = ref.Tell("second") // processed by restarted instance

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("restart did not process second message")
	}
	if starts.Load() != 2 {
		t.Fatalf("factory invoked %d times, want 2", starts.Load())
	}
}

func TestSupervisorStopsAfterMaxRestarts(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var starts atomic.Int32
	ref, _ := sys.Spawn(Spec{
		Name: "always-fails",
		Factory: func() Actor {
			starts.Add(1)
			return ActorFunc(func(ctx Context, msg any) error {
				return errors.New("broken")
			})
		},
		Supervisor: supervisor.NewRestart(2, time.Minute, time.Millisecond, time.Millisecond),
	})

	// Send 5 messages; supervisor should stop after 3 failures (initial + 2 restarts).
	for i := 0; i < 5; i++ {
		_ = ref.Tell(i)
	}
	// Wait for actor to terminate.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := ref.Tell("probe"); err != nil {
			// Mailbox closed — actor has stopped.
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("actor did not stop after max restarts (starts=%d)", starts.Load())
}

func TestStopRefClosesMailbox(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	ref, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(Context, any) error { return nil })
		},
	})

	if err := ref.Stop(); err != nil {
		t.Fatal(err)
	}
	// Give the runloop a moment to exit.
	time.Sleep(20 * time.Millisecond)
	if err := ref.Tell("after stop"); err == nil {
		t.Fatal("Tell after Stop should fail")
	}
}

func TestWatchDeliversTerminatedOnClean(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	terminated := make(chan Terminated, 1)
	watcher, _ := sys.Spawn(Spec{
		Name: "watcher",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				switch m := msg.(type) {
				case Ref:
					return ctx.Watch(m)
				case Terminated:
					terminated <- m
				}
				return nil
			})
		},
	})

	target, _ := sys.Spawn(Spec{
		Name: "target",
		Factory: func() Actor {
			return ActorFunc(func(Context, any) error { return nil })
		},
	})

	_ = watcher.Tell(target)
	time.Sleep(20 * time.Millisecond) // let Watch subscribe
	_ = target.Stop()

	select {
	case term := <-terminated:
		if term.PID != target.PID() {
			t.Fatalf("Terminated.PID = %v, want %v", term.PID, target.PID())
		}
		if term.Reason != nil {
			t.Fatalf("clean stop reason = %v, want nil", term.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("Terminated not delivered")
	}
}

func TestWatchDeliversTerminatedWithReasonOnFailure(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	terminated := make(chan Terminated, 1)
	watcher, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				switch m := msg.(type) {
				case Ref:
					return ctx.Watch(m)
				case Terminated:
					terminated <- m
				}
				return nil
			})
		},
	})

	target, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(Context, any) error {
				return errors.New("broken")
			})
		},
		Supervisor: supervisor.NewStop(),
	})

	_ = watcher.Tell(target)
	time.Sleep(20 * time.Millisecond)
	_ = target.Tell("trigger failure")

	select {
	case term := <-terminated:
		if term.Reason == nil {
			t.Fatal("Terminated.Reason is nil on failed stop")
		}
	case <-time.After(time.Second):
		t.Fatal("Terminated not delivered")
	}
}

func TestUnwatch(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	terminated := make(chan struct{}, 1)
	var watcherRef Ref
	watcher, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				switch m := msg.(type) {
				case Ref:
					return ctx.Watch(m)
				case struct{ Target Ref }:
					return ctx.Unwatch(m.Target)
				case Terminated:
					terminated <- struct{}{}
				}
				return nil
			})
		},
	})
	watcherRef = watcher

	target, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(Context, any) error { return nil })
		},
	})

	_ = watcherRef.Tell(target)
	time.Sleep(10 * time.Millisecond)
	_ = watcherRef.Tell(struct{ Target Ref }{Target: target})
	time.Sleep(10 * time.Millisecond)
	_ = target.Stop()

	select {
	case <-terminated:
		t.Fatal("Unwatch did not prevent Terminated delivery")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWatchAlreadyStoppedDeliversTerminated(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	target, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(Context, any) error { return nil })
		},
	})
	_ = target.Stop()
	// Wait for target's runloop to fully exit.
	time.Sleep(20 * time.Millisecond)

	terminated := make(chan Terminated, 1)
	watcher, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				switch m := msg.(type) {
				case Ref:
					return ctx.Watch(m)
				case Terminated:
					terminated <- m
				}
				return nil
			})
		},
	})
	_ = watcher.Tell(target)

	select {
	case term := <-terminated:
		if term.PID != target.PID() {
			t.Fatalf("PID mismatch: %v vs %v", term.PID, target.PID())
		}
	case <-time.After(time.Second):
		t.Fatal("late Watch did not receive Terminated")
	}
}

func TestEscalateNotifiesParent(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	failed := make(chan Failed, 1)
	parent, _ := sys.Spawn(Spec{
		Name: "parent",
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				switch m := msg.(type) {
				case string:
					_, err := ctx.Spawn(Spec{
						Name: "kid",
						Factory: func() Actor {
							return ActorFunc(func(Context, any) error {
								panic("boom")
							})
						},
						Supervisor: escalateAlways{},
					})
					if err != nil {
						return err
					}
				case Failed:
					failed <- m
				}
				return nil
			})
		},
	})

	_ = parent.Tell("spawn kid")
	time.Sleep(20 * time.Millisecond)

	// Find the kid and kick it off to panic.
	kid, ok := sys.Lookup("/user/parent/kid")
	if !ok {
		t.Fatal("kid not registered")
	}
	_ = kid.Tell("go")

	select {
	case f := <-failed:
		if f.Child != kid.PID() {
			t.Fatalf("Failed.Child = %v, want %v", f.Child, kid.PID())
		}
	case <-time.After(time.Second):
		t.Fatal("parent did not receive Failed")
	}
}

// escalateAlways is a test-only strategy that always returns Escalate.
type escalateAlways struct{}

func (escalateAlways) Decide(any, int, time.Time) supervisor.Decision {
	return supervisor.Decision{Directive: supervisor.Escalate}
}

func TestConcurrentTells(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	var count atomic.Int64
	const N = 1000
	done := make(chan struct{})

	ref, _ := sys.Spawn(Spec{
		Factory: func() Actor {
			return ActorFunc(func(ctx Context, msg any) error {
				if count.Add(1) == N {
					close(done)
				}
				return nil
			})
		},
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < N/16; j++ {
				_ = ref.Tell(j)
			}
		}()
	}
	wg.Wait()

	// Send the final few messages if N isn't divisible by 16.
	for i := (N / 16) * 16; i < N; i++ {
		_ = ref.Tell(i)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("only processed %d / %d messages", count.Load(), N)
	}
}
