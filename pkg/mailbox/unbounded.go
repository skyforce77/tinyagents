package mailbox

import (
	"context"
	"sync"
)

// unbounded is a FIFO queue with no capacity limit. It uses a mutex-protected
// slice plus a 1-buffered notify channel so Dequeue can wait on ctx.Done().
type unbounded struct {
	mu     sync.Mutex
	items  []Envelope
	notify chan struct{}
	closed bool
}

func newUnbounded() *unbounded {
	return &unbounded{notify: make(chan struct{}, 1)}
}

func (u *unbounded) Enqueue(_ context.Context, env Envelope) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return ErrMailboxClosed
	}
	u.items = append(u.items, env)
	u.signalLocked()
	return nil
}

func (u *unbounded) Dequeue(ctx context.Context) (Envelope, error) {
	for {
		u.mu.Lock()
		if len(u.items) > 0 {
			e := u.items[0]
			u.items[0] = Envelope{}
			u.items = u.items[1:]
			u.mu.Unlock()
			return e, nil
		}
		closed := u.closed
		u.mu.Unlock()
		if closed {
			return Envelope{}, ErrMailboxClosed
		}
		select {
		case <-u.notify:
		case <-ctx.Done():
			return Envelope{}, ctx.Err()
		}
	}
}

func (u *unbounded) Len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.items)
}

func (u *unbounded) Cap() int { return 0 }

// Close marks the mailbox closed and wakes any waiting Dequeue callers.
// Closing is idempotent. After Close, Enqueue returns ErrMailboxClosed and
// Dequeue drains remaining items, then returns ErrMailboxClosed.
func (u *unbounded) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return nil
	}
	u.closed = true
	close(u.notify) // wakes all waiters; safe because signalLocked checks closed.
	return nil
}

// signalLocked pulses the notify channel non-blockingly. Caller must hold u.mu.
func (u *unbounded) signalLocked() {
	if u.closed {
		return
	}
	select {
	case u.notify <- struct{}{}:
	default:
	}
}
