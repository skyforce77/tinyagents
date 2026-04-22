// Package supervisor defines restart strategies applied when an actor's
// Receive panics or returns a non-nil error.
//
// A Strategy is pure: given the failure count and time window, it returns a
// Decision. The actor runtime is responsible for honoring the decision
// (delaying the restart, recreating the Actor via its factory, etc.).
package supervisor

import "time"

// Directive tells the runtime what to do with a failed actor.
type Directive int

const (
	// Resume ignores the failure and keeps the actor running.
	Resume Directive = iota
	// Restart recreates the actor instance via its factory.
	Restart
	// Stop terminates the actor without restarting.
	Stop
	// Escalate asks the parent to decide (reserved for M1; treated as Stop for now).
	Escalate
)

// Decision is the result of consulting a Strategy.
type Decision struct {
	Directive Directive
	Delay     time.Duration // only meaningful for Restart
}

// Strategy decides the Decision given a failure.
type Strategy interface {
	Decide(cause any, failureCount int, windowStart time.Time) Decision
}

// Default returns the strategy used when Spec.Supervisor is nil: restart up to
// 5 times within a 1-minute window, with exponential backoff between 50ms and 1s.
func Default() Strategy {
	return NewRestart(5, time.Minute, 50*time.Millisecond, time.Second)
}
