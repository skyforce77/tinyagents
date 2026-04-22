package llm

import (
	"context"
	"sync"
	"time"
)

// RateLimit caps the average request rate at one every interval (so
// rps = 1/interval.Seconds()) with a small burst allowance. It is a plain
// token bucket implemented under a mutex — sufficient for moderate
// per-provider traffic without pulling in a third-party dependency.
//
// Stream and Models share the same bucket as Chat and Embed; if that is
// inappropriate, wrap only the methods you care about in a custom
// Middleware.
func RateLimit(interval time.Duration, burst int) Middleware {
	if interval <= 0 {
		interval = time.Second
	}
	if burst < 1 {
		burst = 1
	}
	return func(next Provider) Provider {
		return &rlMW{
			next:     next,
			interval: interval,
			burst:    burst,
			tokens:   burst,
			last:     time.Now(),
		}
	}
}

type rlMW struct {
	next     Provider
	interval time.Duration
	burst    int

	mu     sync.Mutex
	tokens int
	last   time.Time
}

func (r *rlMW) Name() string { return r.next.Name() }

func (r *rlMW) acquire(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		// Refill tokens based on elapsed time.
		earned := int(now.Sub(r.last) / r.interval)
		if earned > 0 {
			r.tokens += earned
			if r.tokens > r.burst {
				r.tokens = r.burst
			}
			r.last = r.last.Add(time.Duration(earned) * r.interval)
		}
		if r.tokens > 0 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}
		nextToken := r.last.Add(r.interval)
		r.mu.Unlock()
		wait := time.Until(nextToken)
		if wait <= 0 {
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

func (r *rlMW) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if err := r.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	return r.next.Chat(ctx, req)
}

func (r *rlMW) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return r.next.Stream(ctx, req)
}

func (r *rlMW) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if err := r.acquire(ctx); err != nil {
		return EmbedResponse{}, err
	}
	return r.next.Embed(ctx, req)
}

func (r *rlMW) Models(ctx context.Context) ([]Model, error) {
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return r.next.Models(ctx)
}
