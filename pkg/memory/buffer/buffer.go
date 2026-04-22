package buffer

import (
	"context"
	"fmt"
	"sync"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/memory"
)

// Buffer is an in-memory ring of llm.Message values.
type Buffer struct {
	mu    sync.Mutex
	items []llm.Message // ring: items[(start+i) % len(items)]
	start int
	size  int
	cap   int
}

// New returns a Buffer with the given capacity. capacity must be >= 1.
func New(capacity int) *Buffer {
	if capacity < 1 {
		panic(fmt.Sprintf("buffer capacity must be >= 1, got %d", capacity))
	}
	return &Buffer{
		items: make([]llm.Message, capacity),
		cap:   capacity,
	}
}

// Append adds a message to the buffer. If the buffer is full, the oldest
// message is evicted.
func (b *Buffer) Append(_ context.Context, m llm.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.size < b.cap {
		b.items[(b.start+b.size)%b.cap] = m
		b.size++
	} else {
		b.items[b.start] = m
		b.start = (b.start + 1) % b.cap
	}
	return nil
}

// Window returns up to n messages, oldest first. n <= 0 or n > size returns
// everything currently stored (chronological order).
func (b *Buffer) Window(_ context.Context, n int) ([]llm.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := b.size
	if n > 0 && n < b.size {
		count = n
	}
	result := make([]llm.Message, count)
	for i := 0; i < count; i++ {
		idx := (b.start + b.size - count + i) % b.cap
		result[i] = b.items[idx]
	}
	return result, nil
}

// Search is not supported by Buffer; it returns (nil, memory.ErrNotSupported).
func (b *Buffer) Search(context.Context, string, int) ([]llm.Message, error) {
	return nil, memory.ErrNotSupported
}

// Compile-time check.
var _ memory.Memory = (*Buffer)(nil)
