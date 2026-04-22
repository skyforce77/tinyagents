// Package cluster exposes a thin membership + failure-detection
// abstraction. Implementations sit behind the Cluster interface so
// pkg/registry and pkg/crdt/replicator treat local single-node clusters
// identically to gossip-based production clusters.
//
// The Cluster surface intentionally mirrors hashicorp/memberlist: Join,
// Leave, Members, and an event subscription for Joined/Left/Updated
// lifecycle notifications. Cluster is *not* a transport — it only
// knows who is in the ring; pkg/transport handles message delivery.
package cluster

import "context"

// Cluster is the membership surface. Every tinyagents System holds at
// most one Cluster; by default it's cluster/local.
type Cluster interface {
	// Join adds this node to the cluster, contacting the given seed
	// addresses. Implementations that don't care about addresses
	// (like local) can ignore them. Join is idempotent — calling it
	// again on an already-joined cluster is a no-op.
	Join(ctx context.Context, seeds ...string) error
	// Leave announces this node's departure and blocks until peers
	// acknowledge (best-effort). Leave is safe to call before Join
	// (no-op) and multiple times.
	Leave(ctx context.Context) error
	// Members returns a snapshot of the current membership view,
	// including this node. The slice is owned by the caller.
	Members() []Member
	// LocalNode returns the Member describing this node.
	LocalNode() Member
	// Subscribe registers an EventHandler that receives cluster
	// events. Returns an unsubscribe function. Multiple subscribers
	// are allowed; events are delivered sequentially per subscriber.
	Subscribe(h EventHandler) (unsubscribe func())
	// Close shuts down the cluster implementation and releases
	// resources. Subsequent Members() returns the last known view.
	Close() error
}

// Member describes one participant in the cluster. The ID is stable
// across restarts when set by the operator; Address is the gossip
// endpoint (empty for local). Meta is opaque key/value metadata the
// node gossips with its heartbeat — callers use it for capability
// advertisements (e.g. "gpu=true").
type Member struct {
	ID      string
	Address string
	Meta    map[string]string
}

// EventKind enumerates membership transitions.
type EventKind int

const (
	MemberJoined EventKind = iota + 1
	MemberLeft
	MemberUpdated // meta changed or member came back after being flagged suspect
)

// Event is delivered to subscribers on every membership change.
type Event struct {
	Kind   EventKind
	Member Member
}

// EventHandler processes Events. It must not block for long; implementations
// should offload heavy work to a goroutine.
type EventHandler func(Event)
