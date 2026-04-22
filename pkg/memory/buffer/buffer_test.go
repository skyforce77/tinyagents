package buffer

import (
	"context"
	"sync"
	"testing"

	"github.com/skyforce77/TinyActors/pkg/memory"
)

func TestAppendAndWindow(t *testing.T) {
	buf := New(10)
	ctx := context.Background()

	// Append 3 messages
	msg1 := memory.Message{Role: "user", Content: "message 1"}
	msg2 := memory.Message{Role: "assistant", Content: "message 2"}
	msg3 := memory.Message{Role: "user", Content: "message 3"}

	if err := buf.Append(ctx, msg1); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := buf.Append(ctx, msg2); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := buf.Append(ctx, msg3); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Window(3) should return all 3 in order (oldest first)
	msgs, err := buf.Window(ctx, 3)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(msgs))
	}
	if msgs[0] != msg1 || msgs[1] != msg2 || msgs[2] != msg3 {
		t.Errorf("Messages in wrong order or corrupted")
	}
}

func TestCapacityEviction(t *testing.T) {
	buf := New(2)
	ctx := context.Background()

	msg1 := memory.Message{Role: "user", Content: "message 1"}
	msg2 := memory.Message{Role: "assistant", Content: "message 2"}
	msg3 := memory.Message{Role: "user", Content: "message 3"}

	if err := buf.Append(ctx, msg1); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := buf.Append(ctx, msg2); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := buf.Append(ctx, msg3); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Window(3) with capacity 2 should return only last 2 (msg2, msg3)
	msgs, err := buf.Window(ctx, 3)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages (eviction happened), got %d", len(msgs))
	}
	if msgs[0] != msg2 || msgs[1] != msg3 {
		t.Errorf("Messages in wrong order or corrupted. Got %v, %v", msgs[0], msgs[1])
	}
}

func TestWindowN(t *testing.T) {
	buf := New(5)
	ctx := context.Background()

	// Append 4 messages
	msgs := make([]memory.Message, 4)
	for i := 0; i < 4; i++ {
		msgs[i] = memory.Message{Role: "user", Content: string(rune(int('0') + i))}
		if err := buf.Append(ctx, msgs[i]); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Window(2) should return the 2 most recent in chronological order
	result, err := buf.Window(ctx, 2)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}
	// The 2 most recent are msgs[2] and msgs[3]
	if result[0] != msgs[2] || result[1] != msgs[3] {
		t.Errorf("Expected last 2 messages, got %v, %v", result[0], result[1])
	}
}

func TestSearchUnsupported(t *testing.T) {
	buf := New(10)
	ctx := context.Background()

	result, err := buf.Search(ctx, "test", 5)
	if err != memory.ErrNotSupported {
		t.Errorf("Expected ErrNotSupported, got %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
}

func TestConcurrentAppends(t *testing.T) {
	buf := New(1000)
	ctx := context.Background()

	const numGoroutines = 8
	const appends = 100
	var wg sync.WaitGroup

	// Launch 8 goroutines, each appending 100 messages
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < appends; i++ {
				msg := memory.Message{
					Role:    "user",
					Content: string(rune('A' + (goroutineID % 26))) + string(rune('0'+(i%10))),
				}
				if err := buf.Append(ctx, msg); err != nil {
					t.Errorf("Append failed: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	// Window all messages
	result, err := buf.Window(ctx, 0)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}

	// We appended 8 * 100 = 800 messages to a capacity-1000 buffer
	// So we should have exactly 800 messages
	if len(result) != 800 {
		t.Errorf("Expected 800 messages, got %d", len(result))
	}

	// Verify all are memory.Message values with expected structure
	for _, msg := range result {
		if msg.Role != "user" {
			t.Errorf("Expected user role, got %v", msg.Role)
		}
		if len(msg.Content) != 2 {
			t.Errorf("Expected Content length 2, got %d", len(msg.Content))
		}
	}
}

func TestWindowAllMessages(t *testing.T) {
	buf := New(5)
	ctx := context.Background()

	// Append 3 messages
	msg1 := memory.Message{Role: "user", Content: "message 1"}
	msg2 := memory.Message{Role: "assistant", Content: "message 2"}
	msg3 := memory.Message{Role: "user", Content: "message 3"}

	buf.Append(ctx, msg1)
	buf.Append(ctx, msg2)
	buf.Append(ctx, msg3)

	// Window(0) should return everything
	msgs, err := buf.Window(ctx, 0)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("Expected 3 messages with Window(0), got %d", len(msgs))
	}
}

func TestWindowNegative(t *testing.T) {
	buf := New(5)
	ctx := context.Background()

	msg1 := memory.Message{Role: "user", Content: "message 1"}
	msg2 := memory.Message{Role: "assistant", Content: "message 2"}

	buf.Append(ctx, msg1)
	buf.Append(ctx, msg2)

	// Window(-1) should return everything
	msgs, err := buf.Window(ctx, -1)
	if err != nil {
		t.Fatalf("Window failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("Expected 2 messages with Window(-1), got %d", len(msgs))
	}
}
