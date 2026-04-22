package crdt

import (
	"encoding/json"
	"errors"
	"sync"
)

// PNCounter is a positive/negative counter CRDT built from two GCounters:
// one for increments (positive) and one for decrements (negative). The
// observable value is positive.Value() - negative.Value(), which may be
// negative.
//
// Merge rule: delegate to GCounter.Merge for both inner counters.
type PNCounter struct {
	mu       sync.Mutex
	Node     NodeID `json:"node"`
	positive GCounter
	negative GCounter
}

// NewPNCounter creates a PNCounter for the given node.
func NewPNCounter(node NodeID) *PNCounter {
	return &PNCounter{
		Node:     node,
		positive: GCounter{Node: node, Counts: make(map[NodeID]uint64)},
		negative: GCounter{Node: node, Counts: make(map[NodeID]uint64)},
	}
}

// Inc adds n to the positive counter.
func (c *PNCounter) Inc(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.positive.Counts[c.Node] += n
}

// Dec adds n to the negative counter.
func (c *PNCounter) Dec(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.negative.Counts[c.Node] += n
}

// Value returns the net count: sum(positive) - sum(negative).
func (c *PNCounter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var pos, neg uint64
	for _, v := range c.positive.Counts {
		pos += v
	}
	for _, v := range c.negative.Counts {
		neg += v
	}
	return int64(pos) - int64(neg)
}

// Merge absorbs the state of another PNCounter by merging the inner
// GCounters pairwise. Merge returns an error if other is nil or not a
// *PNCounter.
func (c *PNCounter) Merge(other CRDT) error {
	if other == nil {
		return errors.New("pncounter: cannot merge nil CRDT")
	}
	o, ok := other.(*PNCounter)
	if !ok {
		return errors.New("pncounter: type mismatch on Merge")
	}

	// Snapshot the incoming counter's maps under its lock.
	o.mu.Lock()
	posSnap := make(map[NodeID]uint64, len(o.positive.Counts))
	negSnap := make(map[NodeID]uint64, len(o.negative.Counts))
	for k, v := range o.positive.Counts {
		posSnap[k] = v
	}
	for k, v := range o.negative.Counts {
		negSnap[k] = v
	}
	o.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range posSnap {
		if v > c.positive.Counts[k] {
			c.positive.Counts[k] = v
		}
	}
	for k, v := range negSnap {
		if v > c.negative.Counts[k] {
			c.negative.Counts[k] = v
		}
	}
	return nil
}

// Snapshot serializes the full counter state to JSON.
func (c *PNCounter) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	type wire struct {
		Node     NodeID            `json:"node"`
		Positive map[NodeID]uint64 `json:"positive"`
		Negative map[NodeID]uint64 `json:"negative"`
	}
	return json.Marshal(wire{
		Node:     c.Node,
		Positive: c.positive.Counts,
		Negative: c.negative.Counts,
	})
}

// Restore replaces the counter state with the output of a prior Snapshot.
func (c *PNCounter) Restore(data []byte) error {
	type wire struct {
		Node     NodeID            `json:"node"`
		Positive map[NodeID]uint64 `json:"positive"`
		Negative map[NodeID]uint64 `json:"negative"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Node = w.Node
	c.positive = GCounter{Node: w.Node, Counts: w.Positive}
	c.negative = GCounter{Node: w.Node, Counts: w.Negative}
	if c.positive.Counts == nil {
		c.positive.Counts = make(map[NodeID]uint64)
	}
	if c.negative.Counts == nil {
		c.negative.Counts = make(map[NodeID]uint64)
	}
	return nil
}
