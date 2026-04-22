package actor

import (
	"github.com/skyforce77/tinyagents/pkg/mailbox"
	"github.com/skyforce77/tinyagents/pkg/supervisor"
)

// Spec describes how to spawn an actor. Only Factory is required; the rest
// have sensible defaults (auto-generated name, unbounded mailbox, default
// restart supervisor).
type Spec struct {
	// Name is the last path segment. If empty, the runtime generates a
	// unique name under the parent (or root) namespace.
	Name string

	// Factory creates a fresh Actor instance. It is called once at spawn
	// and again on every supervisor-driven restart — do not capture
	// externally-shared state that assumes single-instance ownership.
	Factory func() Actor

	// Mailbox configures capacity and backpressure policy. Zero value
	// means unbounded with Block policy (a moot policy at capacity 0).
	Mailbox mailbox.Config

	// Supervisor chooses the failure strategy. Nil means supervisor.Default().
	Supervisor supervisor.Strategy
}
