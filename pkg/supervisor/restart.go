package supervisor

import "time"

// restart implements a bounded-retry strategy with exponential backoff.
type restart struct {
	maxRestarts int
	window      time.Duration
	baseDelay   time.Duration
	maxDelay    time.Duration
}

// NewRestart builds a Restart strategy. maxRestarts <= 0 means "always restart".
func NewRestart(maxRestarts int, window, baseDelay, maxDelay time.Duration) Strategy {
	if baseDelay <= 0 {
		baseDelay = 10 * time.Millisecond
	}
	if maxDelay < baseDelay {
		maxDelay = baseDelay
	}
	return &restart{
		maxRestarts: maxRestarts,
		window:      window,
		baseDelay:   baseDelay,
		maxDelay:    maxDelay,
	}
}

func (r *restart) Decide(_ any, failureCount int, windowStart time.Time) Decision {
	// Too many failures within the window → stop the actor.
	if r.maxRestarts > 0 &&
		failureCount > r.maxRestarts &&
		time.Since(windowStart) <= r.window {
		return Decision{Directive: Stop}
	}
	return Decision{Directive: Restart, Delay: r.backoff(failureCount)}
}

func (r *restart) backoff(attempt int) time.Duration {
	if attempt <= 1 {
		return r.baseDelay
	}
	// Exponential: base * 2^(attempt-1), capped at maxDelay.
	d := r.baseDelay
	for i := 1; i < attempt && d < r.maxDelay; i++ {
		d *= 2
	}
	if d > r.maxDelay {
		d = r.maxDelay
	}
	return d
}

// NewStop returns a strategy that always stops the actor on failure.
func NewStop() Strategy { return stopStrategy{} }

type stopStrategy struct{}

func (stopStrategy) Decide(any, int, time.Time) Decision { return Decision{Directive: Stop} }
