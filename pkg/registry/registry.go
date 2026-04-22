// Package registry exposes a read-only view of a running actor System:
// which actors exist, how to resolve one by path, how many are alive.
//
// The single-node registry is served directly by the System in the actor
// package — *actor.System satisfies Registry implicitly. Once clustering
// lands, a cluster-aware implementation will live here so user code can
// target Registry abstractly.
package registry

import "github.com/skyforce77/tinyagents/pkg/actor"

// Registry is the minimum contract any registry must provide.
type Registry interface {
	// Lookup returns a Ref addressing the actor at the given path, or
	// (nil, false) if no such actor exists.
	Lookup(path string) (actor.Ref, bool)
	// List returns every known Ref. Order is unspecified.
	List() []actor.Ref
}
