package buffer

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/memory"
)

func TestAppendAndWindow(t *testing.T) {
	t.Parallel()
	buf := New(10)
	ctx := context.Background()

	msg1 := llm.Message{Role: llm.RoleUser, Content: "message 1"}
	msg2 := llm.Message{Role: llm.RoleAssistant, Content: "message 2"}
	msg3 := llm.Message{Role: llm.RoleUser, Content: "message 3"}

	for _, m := range []llm.Message{msg1, msg2, msg3} {
		if err := buf.Append(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	msgs, err := buf.Window(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d, want 3", len(msgs))
	}
	if msgs[0].Content != "message 1" || msgs[1].Content != "message 2" || msgs[2].Content != "message 3" {
		t.Fatalf("out of order: %+v", msgs)
	}
}

func TestCapacityEviction(t *testing.T) {
	t.Parallel()
	buf := New(2)
	ctx := context.Background()

	for i, c := range []string{"one", "two", "three"} {
		_ = buf.Append(ctx, llm.Message{Role: llm.RoleUser, Content: c})
		_ = i
	}
	msgs, err := buf.Window(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d, want 2 (oldest evicted)", len(msgs))
	}
	if msgs[0].Content != "two" || msgs[1].Content != "three" {
		t.Fatalf("out of order after eviction: %+v", msgs)
	}
}

func TestWindowN(t *testing.T) {
	t.Parallel()
	buf := New(5)
	ctx := context.Background()
	for _, c := range []string{"a", "b", "c", "d"} {
		_ = buf.Append(ctx, llm.Message{Role: llm.RoleUser, Content: c})
	}
	msgs, err := buf.Window(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d, want 2", len(msgs))
	}
	if msgs[0].Content != "c" || msgs[1].Content != "d" {
		t.Fatalf("wrong last-2 window: %+v", msgs)
	}
}

func TestWindowAllMessages(t *testing.T) {
	t.Parallel()
	buf := New(5)
	ctx := context.Background()
	for _, c := range []string{"a", "b", "c"} {
		_ = buf.Append(ctx, llm.Message{Role: llm.RoleUser, Content: c})
	}
	msgs, err := buf.Window(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("Window(0) len = %d, want 3", len(msgs))
	}
	msgs, _ = buf.Window(ctx, -1)
	if len(msgs) != 3 {
		t.Fatalf("Window(-1) len = %d, want 3", len(msgs))
	}
}

func TestSearchUnsupported(t *testing.T) {
	t.Parallel()
	buf := New(10)
	result, err := buf.Search(context.Background(), "test", 5)
	if !errors.Is(err, memory.ErrNotSupported) {
		t.Fatalf("got %v, want ErrNotSupported", err)
	}
	if result != nil {
		t.Fatalf("got %v, want nil", result)
	}
}

func TestConcurrentAppends(t *testing.T) {
	t.Parallel()
	buf := New(1000)
	ctx := context.Background()

	const numGoroutines = 8
	const appends = 100
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < appends; i++ {
				_ = buf.Append(ctx, llm.Message{Role: llm.RoleUser, Content: "x"})
			}
		}(g)
	}
	wg.Wait()

	result, err := buf.Window(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != numGoroutines*appends {
		t.Fatalf("got %d, want %d", len(result), numGoroutines*appends)
	}
	for _, m := range result {
		if m.Role != llm.RoleUser {
			t.Fatalf("unexpected role %q", m.Role)
		}
	}
}
