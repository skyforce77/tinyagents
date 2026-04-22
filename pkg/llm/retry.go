package llm

import (
	"context"
	"time"
)

// BackoffFunc returns the delay before the nth attempt (1-indexed).
type BackoffFunc func(attempt int) time.Duration

// ExponentialBackoff produces a capped exponential backoff starting at base
// and doubling each attempt, never exceeding max. Attempt 1 returns base.
func ExponentialBackoff(base, max time.Duration) BackoffFunc {
	if base <= 0 {
		base = 10 * time.Millisecond
	}
	if max < base {
		max = base
	}
	return func(attempt int) time.Duration {
		if attempt <= 1 {
			return base
		}
		d := base
		for i := 1; i < attempt && d < max; i++ {
			d *= 2
		}
		if d > max {
			d = max
		}
		return d
	}
}

// Retry wraps a Provider so that Chat and Embed retry transient errors up
// to maxAttempts. Stream and Models are left alone — streams are stateful
// and catalogs are usually cached.
//
// shouldRetry decides whether err is worth retrying. When nil, every error
// is retried.
func Retry(maxAttempts int, backoff BackoffFunc, shouldRetry func(error) bool) Middleware {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if shouldRetry == nil {
		shouldRetry = func(error) bool { return true }
	}
	return func(next Provider) Provider {
		return &retryMW{next: next, max: maxAttempts, backoff: backoff, shouldRetry: shouldRetry}
	}
}

type retryMW struct {
	next        Provider
	max         int
	backoff     BackoffFunc
	shouldRetry func(error) bool
}

func (r *retryMW) Name() string { return r.next.Name() }

func (r *retryMW) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	var resp ChatResponse
	var err error
	for attempt := 1; attempt <= r.max; attempt++ {
		resp, err = r.next.Chat(ctx, req)
		if err == nil || !r.shouldRetry(err) || attempt == r.max {
			return resp, err
		}
		if werr := r.wait(ctx, attempt); werr != nil {
			return ChatResponse{}, werr
		}
	}
	return resp, err
}

func (r *retryMW) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	var resp EmbedResponse
	var err error
	for attempt := 1; attempt <= r.max; attempt++ {
		resp, err = r.next.Embed(ctx, req)
		if err == nil || !r.shouldRetry(err) || attempt == r.max {
			return resp, err
		}
		if werr := r.wait(ctx, attempt); werr != nil {
			return EmbedResponse{}, werr
		}
	}
	return resp, err
}

func (r *retryMW) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	return r.next.Stream(ctx, req)
}

func (r *retryMW) Models(ctx context.Context) ([]Model, error) {
	return r.next.Models(ctx)
}

func (r *retryMW) wait(ctx context.Context, attempt int) error {
	if r.backoff == nil {
		return nil
	}
	delay := r.backoff(attempt)
	if delay <= 0 {
		return nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
