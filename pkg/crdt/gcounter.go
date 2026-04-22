package crdt

import (
	"encoding/json"
	"errors"
	"sync"
)

// GCounter is a grow-only counter CRDT. Each node owns one slot in the
// Counts map and may only increment its own slot. The global value is
// the sum of all slots.
//
// Merge rule: for each NodeID present in either operand, keep the
// maximum of the two counts. This makes Merge idempotent, commutative,
// and associative.
type GCounter struct {
	mu     sync.Mutex
	Node   NodeID            `json:"node"`
	Counts map[NodeID]uint64 `json:"counts"`
}

// NewGCounter creates a GCounter for the given node with an empty count map.
func NewGCounter(node NodeID) *GCounter {
	return &GCounter{
		Node:   node,
		Counts: make(map[NodeID]uint64),
	}
}

// Inc adds n to the local node's counter.
func (c *GCounter) Inc(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Counts[c.Node] += n
}

// Value returns the sum of all per-node counts.
func (c *GCounter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total uint64
	for _, v := range c.Counts {
		total += v
	}
	return total
}

// Merge absorbs the state of another GCounter. For each NodeID in either
// counter the higher count survives. Merge returns an error if other is
// nil or not a *GCounter.
func (c *GCounter) Merge(other CRDT) error {
	if other == nil {
		return errors.New("gcounter: cannot merge nil CRDT")
	}
	o, ok := other.(*GCounter)
	if !ok {
		return errors.New("gcounter: type mismatch on Merge")
	}

	// Snapshot the incoming counter under its own lock first, then apply
	// to c under c's lock. This avoids deadlock when two replicas merge
	// each other concurrently.
	o.mu.Lock()
	snap := make(map[NodeID]uint64, len(o.Counts))
	for k, v := range o.Counts {
		snap[k] = v
	}
	o.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range snap {
		if v > c.Counts[k] {
			c.Counts[k] = v
		}
	}
	return nil
}

// Snapshot serializes the full counter state to JSON.
func (c *GCounter) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	type wire struct {
		Node   NodeID            `json:"node"`
		Counts map[NodeID]uint64 `json:"counts"`
	}
	return json.Marshal(wire{Node: c.Node, Counts: c.Counts})
}

// Restore replaces the counter state with the output of a prior Snapshot.
func (c *GCounter) Restore(data []byte) error {
	type wire struct {
		Node   NodeID            `json:"node"`
		Counts map[NodeID]uint64 `json:"counts"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Node = w.Node
	c.Counts = w.Counts
	if c.Counts == nil {
		c.Counts = make(map[NodeID]uint64)
	}
	return nil
}
