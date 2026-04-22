package mailbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUnboundedEnqueueDequeue(t *testing.T) {
	t.Parallel()
	mb := New(Config{})
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		if err := mb.Enqueue(ctx, Envelope{Msg: i}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	if mb.Len() != 100 {
		t.Fatalf("Len = %d, want 100", mb.Len())
	}
	for i := 0; i < 100; i++ {
		got, err := mb.Dequeue(ctx)
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if got.Msg != i {
			t.Fatalf("Dequeue %d: Msg = %v, want %d", i, got.Msg, i)
		}
	}
}

func TestUnboundedDequeueBlocksThenReceives(t *testing.T) {
	t.Parallel()
	mb := New(Config{})
	ctx := context.Background()

	done := make(chan Envelope)
	go func() {
		e, err := mb.Dequeue(ctx)
		if err != nil {
			t.Errorf("Dequeue: %v", err)
		}
		done <- e
	}()

	// Give the goroutine time to reach the select.
	time.Sleep(10 * time.Millisecond)
	if err := mb.Enqueue(ctx, Envelope{Msg: "hi"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case e := <-done:
		if e.Msg != "hi" {
			t.Fatalf("got %v, want hi", e.Msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Dequeue never unblocked")
	}
}

func TestDequeueCancelled(t *testing.T) {
	t.Parallel()
	mb := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := mb.Dequeue(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dequeue err = %v, want DeadlineExceeded", err)
	}
}

func TestClosedMailbox(t *testing.T) {
	t.Parallel()
	mb := New(Config{})
	ctx := context.Background()

	if err := mb.Enqueue(ctx, Envelope{Msg: 1}); err != nil {
		t.Fatal(err)
	}
	if err := mb.Close(); err != nil {
		t.Fatal(err)
	}
	// Close is idempotent.
	if err := mb.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Dequeue drains the remaining item, then returns ErrMailboxClosed.
	e, err := mb.Dequeue(ctx)
	if err != nil {
		t.Fatalf("first Dequeue after close: %v", err)
	}
	if e.Msg != 1 {
		t.Fatalf("got %v, want 1", e.Msg)
	}
	_, err = mb.Dequeue(ctx)
	if !errors.Is(err, ErrMailboxClosed) {
		t.Fatalf("second Dequeue after close: %v, want ErrMailboxClosed", err)
	}

	if err := mb.Enqueue(ctx, Envelope{Msg: 2}); !errors.Is(err, ErrMailboxClosed) {
		t.Fatalf("Enqueue after close: %v, want ErrMailboxClosed", err)
	}
}

func TestBoundedBlockPolicy(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 2, Policy: Block})
	ctx := context.Background()

	if err := mb.Enqueue(ctx, Envelope{Msg: 1}); err != nil {
		t.Fatal(err)
	}
	if err := mb.Enqueue(ctx, Envelope{Msg: 2}); err != nil {
		t.Fatal(err)
	}
	if mb.Len() != 2 {
		t.Fatalf("Len = %d, want 2", mb.Len())
	}

	// Third Enqueue should block until a Dequeue happens.
	enqDone := make(chan error, 1)
	go func() { enqDone <- mb.Enqueue(ctx, Envelope{Msg: 3}) }()

	select {
	case err := <-enqDone:
		t.Fatalf("Enqueue did not block; returned %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Drain one, unblocking the pending sender.
	if _, err := mb.Dequeue(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-enqDone:
		if err != nil {
			t.Fatalf("pending Enqueue: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending Enqueue never unblocked")
	}
}

func TestBoundedBlockCancel(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 1, Policy: Block})
	ctx := context.Background()
	_ = mb.Enqueue(ctx, Envelope{Msg: 1})

	ctx2, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := mb.Enqueue(ctx2, Envelope{Msg: 2})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}

func TestBoundedDropNewest(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 2, Policy: DropNewest})
	ctx := context.Background()
	_ = mb.Enqueue(ctx, Envelope{Msg: 1})
	_ = mb.Enqueue(ctx, Envelope{Msg: 2})
	if err := mb.Enqueue(ctx, Envelope{Msg: 3}); err != nil {
		t.Fatalf("DropNewest Enqueue returned %v, want nil", err)
	}
	if mb.Len() != 2 {
		t.Fatalf("Len = %d, want 2", mb.Len())
	}
	e1, _ := mb.Dequeue(ctx)
	e2, _ := mb.Dequeue(ctx)
	if e1.Msg != 1 || e2.Msg != 2 {
		t.Fatalf("drained [%v, %v], want [1, 2]", e1.Msg, e2.Msg)
	}
}

func TestBoundedDropOldest(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 2, Policy: DropOldest})
	ctx := context.Background()
	_ = mb.Enqueue(ctx, Envelope{Msg: 1})
	_ = mb.Enqueue(ctx, Envelope{Msg: 2})
	_ = mb.Enqueue(ctx, Envelope{Msg: 3})
	if mb.Len() != 2 {
		t.Fatalf("Len = %d, want 2", mb.Len())
	}
	e1, _ := mb.Dequeue(ctx)
	e2, _ := mb.Dequeue(ctx)
	if e1.Msg != 2 || e2.Msg != 3 {
		t.Fatalf("drained [%v, %v], want [2, 3]", e1.Msg, e2.Msg)
	}
}

func TestBoundedFail(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 1, Policy: Fail})
	ctx := context.Background()
	_ = mb.Enqueue(ctx, Envelope{Msg: 1})
	err := mb.Enqueue(ctx, Envelope{Msg: 2})
	if !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("got %v, want ErrMailboxFull", err)
	}
}

func TestConcurrentProducersConsumers(t *testing.T) {
	t.Parallel()
	mb := New(Config{Capacity: 32, Policy: Block})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const producers = 8
	const consumers = 4
	const perProducer = 500
	total := producers * perProducer

	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if err := mb.Enqueue(ctx, Envelope{Msg: p*perProducer + i}); err != nil {
					t.Errorf("Enqueue: %v", err)
					return
				}
			}
		}(p)
	}

	received := make(chan int, total)
	var cwg sync.WaitGroup
	cwg.Add(consumers)
	for c := 0; c < consumers; c++ {
		go func() {
			defer cwg.Done()
			for {
				e, err := mb.Dequeue(ctx)
				if err != nil {
					return
				}
				received <- e.Msg.(int)
			}
		}()
	}

	wg.Wait()
	// Wait until we've received everything, then cancel consumers.
	got := make(map[int]struct{}, total)
	deadline := time.After(5 * time.Second)
	for len(got) < total {
		select {
		case m := <-received:
			got[m] = struct{}{}
		case <-deadline:
			t.Fatalf("only got %d/%d messages", len(got), total)
		}
	}
	cancel()
	cwg.Wait()
	if len(got) != total {
		t.Fatalf("got %d unique messages, want %d", len(got), total)
	}
}
