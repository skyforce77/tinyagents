package buffer

import (
	"context"
	"fmt"
	"sync"

	"github.com/skyforce77/TinyActors/pkg/memory"
)

// Buffer is an in-memory ring of memory.Message values.
type Buffer struct {
	mu    sync.Mutex
	items []memory.Message // ring: items[(start+i) % len(items)]
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
		items: make([]memory.Message, capacity),
		start: 0,
		size:  0,
		cap:   capacity,
	}
}

// Append adds a message to the buffer. If the buffer is full, the oldest
// message is evicted.
func (b *Buffer) Append(_ context.Context, m memory.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.size < b.cap {
		// Buffer not full yet
		b.items[(b.start+b.size)%b.cap] = m
		b.size++
	} else {
		// Buffer full, overwrite oldest (eviction)
		b.items[b.start] = m
		b.start = (b.start + 1) % b.cap
	}
	return nil
}

// Window returns up to n messages, oldest first. n <= 0 or n > size returns
// everything currently stored (chronological order).
func (b *Buffer) Window(_ context.Context, n int) ([]memory.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Determine how many messages to return
	count := b.size
	if n > 0 && n < b.size {
		count = n
	}

	// Create a copy of the messages in chronological order
	result := make([]memory.Message, count)
	for i := 0; i < count; i++ {
		idx := (b.start + b.size - count + i) % b.cap
		result[i] = b.items[idx]
	}

	return result, nil
}

// Search is not supported by Buffer; it returns (nil, memory.ErrNotSupported).
func (b *Buffer) Search(context.Context, string, int) ([]memory.Message, error) {
	return nil, memory.ErrNotSupported
}

// Compile-time check.
var _ memory.Memory = (*Buffer)(nil)
