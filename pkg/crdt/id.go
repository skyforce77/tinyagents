package crdt

import (
	"sync"
	"time"
)

// NodeID uniquely identifies a replica in the cluster. A UUID or a
// hostname-PID string are both acceptable values; the only requirements
// are stability across restarts and global uniqueness.
type NodeID string

// HybridTimestamp is a (wall-clock, logical counter, node) triple that
// establishes a total order across replicas even when wall-clock time
// skews backward (e.g. NTP corrections).
type HybridTimestamp struct {
	Wall    int64  `json:"wall"`    // unix nanoseconds
	Logical uint32 `json:"logical"` // tiebreaker within the same nanosecond
	Node    NodeID `json:"node"`    // final tiebreaker when wall and logical agree
}

// Before reports whether t precedes b in the hybrid total order.
// Comparison is lexicographic: (Wall, Logical, Node).
func (t HybridTimestamp) Before(b HybridTimestamp) bool {
	if t.Wall != b.Wall {
		return t.Wall < b.Wall
	}
	if t.Logical != b.Logical {
		return t.Logical < b.Logical
	}
	return t.Node < b.Node
}

// After reports whether t follows b in the hybrid total order.
func (t HybridTimestamp) After(b HybridTimestamp) bool {
	return b.Before(t)
}

// Equal reports whether t and b represent the same instant on the same
// node.
func (t HybridTimestamp) Equal(b HybridTimestamp) bool {
	return t.Wall == b.Wall && t.Logical == b.Logical && t.Node == b.Node
}

// HybridClock produces monotonically increasing HybridTimestamps.
// It is safe for concurrent use from multiple goroutines.
type HybridClock struct {
	mu   sync.Mutex
	last HybridTimestamp
}

// Now returns a HybridTimestamp that is strictly greater than every
// previous timestamp issued by this clock. If the system wall clock
// went backward (NTP skew), the logical counter advances instead of
// the wall value, preserving monotonicity.
func (c *HybridClock) Now(node NodeID) HybridTimestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	wall := time.Now().UnixNano()
	var ts HybridTimestamp
	switch {
	case wall > c.last.Wall:
		// Normal case: wall time advanced.
		ts = HybridTimestamp{Wall: wall, Logical: 0, Node: node}
	default:
		// Wall time went backward or did not advance; bump logical counter.
		ts = HybridTimestamp{Wall: c.last.Wall, Logical: c.last.Logical + 1, Node: node}
	}
	c.last = ts
	return ts
}

// Observe updates the clock so that subsequent Now calls dominate ts.
// Call this whenever a timestamp arrives from a remote replica so that
// the local clock stays at least as current as any peer in the cluster.
func (c *HybridClock) Observe(ts HybridTimestamp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts.After(c.last) {
		c.last = ts
	}
}
