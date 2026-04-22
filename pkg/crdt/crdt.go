// Package crdt provides conflict-free replicated data types for
// eventually-consistent state in a distributed tinyagents cluster.
//
// All types are delta-state CRDTs: they expose Merge (idempotent,
// commutative, associative) and serialize to bytes via Snapshot/Restore.
// A Replicator (see pkg/crdt/replicator, M4.7) broadcasts deltas over
// the cluster transport.
package crdt

// CRDT is the common contract every replicated type implements.
type CRDT interface {
	// Merge absorbs state from another replica of the same type.
	// Implementations must enforce type safety; returning an error is
	// preferable to a panic on a mismatched Merge.
	Merge(other CRDT) error
	// Snapshot serializes the full state (for bootstrapping a new node
	// or reconciling after partition).
	Snapshot() ([]byte, error)
	// Restore reconstructs state from Snapshot output. Existing content
	// is replaced.
	Restore([]byte) error
}
