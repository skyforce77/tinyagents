package mailbox

import (
	"context"
	"sync"
)

// bounded is a capacity-limited FIFO. It uses two notify channels so Enqueue
// (under Block policy) and Dequeue can both integrate with ctx cancellation.
type bounded struct {
	mu        sync.Mutex
	items     []Envelope
	cap       int
	policy    BackpressurePolicy
	notifyDeq chan struct{} // signalled when an item is enqueued
	notifyEnq chan struct{} // signalled when an item is dequeued (Block policy)
	closed    bool
}

func newBounded(capacity int, policy BackpressurePolicy) *bounded {
	return &bounded{
		items:     make([]Envelope, 0, capacity),
		cap:       capacity,
		policy:    policy,
		notifyDeq: make(chan struct{}, 1),
		notifyEnq: make(chan struct{}, 1),
	}
}

func (b *bounded) Enqueue(ctx context.Context, env Envelope) error {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return ErrMailboxClosed
		}
		if len(b.items) < b.cap {
			b.items = append(b.items, env)
			b.signalDeqLocked()
			b.mu.Unlock()
			return nil
		}
		// Full — apply policy.
		switch b.policy {
		case DropNewest:
			b.mu.Unlock()
			return nil
		case DropOldest:
			b.items[0] = Envelope{}
			b.items = b.items[1:]
			b.items = append(b.items, env)
			b.signalDeqLocked()
			b.mu.Unlock()
			return nil
		case Fail:
			b.mu.Unlock()
			return ErrMailboxFull
		case Block:
			b.mu.Unlock()
			select {
			case <-b.notifyEnq:
			case <-ctx.Done():
				return ctx.Err()
			}
			// loop: re-check after wake
		default:
			b.mu.Unlock()
			return ErrMailboxFull
		}
	}
}

func (b *bounded) Dequeue(ctx context.Context) (Envelope, error) {
	for {
		b.mu.Lock()
		if len(b.items) > 0 {
			e := b.items[0]
			b.items[0] = Envelope{}
			b.items = b.items[1:]
			b.signalEnqLocked()
			b.mu.Unlock()
			return e, nil
		}
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return Envelope{}, ErrMailboxClosed
		}
		select {
		case <-b.notifyDeq:
		case <-ctx.Done():
			return Envelope{}, ctx.Err()
		}
	}
}

func (b *bounded) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

func (b *bounded) Cap() int { return b.cap }

func (b *bounded) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.notifyDeq)
	close(b.notifyEnq)
	return nil
}

func (b *bounded) signalDeqLocked() {
	if b.closed {
		return
	}
	select {
	case b.notifyDeq <- struct{}{}:
	default:
	}
}

func (b *bounded) signalEnqLocked() {
	if b.closed {
		return
	}
	select {
	case b.notifyEnq <- struct{}{}:
	default:
	}
}
