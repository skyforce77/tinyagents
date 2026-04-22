package crdt

import (
	"encoding/json"
	"errors"
	"sync"
)

// LWWRegister is a last-write-wins register CRDT. Conflicting concurrent
// writes are resolved by comparing HybridTimestamps; if the timestamps
// are equal the higher NodeID wins, ensuring a deterministic total order.
//
// T may be any JSON-serializable type. The value is JSON-encoded in the
// snapshot so that generic type information survives round-trips.
//
// Merge rule: adopt the incoming (value, timestamp) pair if its timestamp
// is strictly greater than the current one, or if the timestamps are equal
// and the incoming Node is lexicographically greater. After merge the local
// HybridClock observes the incoming timestamp so subsequent Now() calls
// dominate it across the cluster.
type LWWRegister[T any] struct {
	mu    sync.Mutex
	Node  NodeID
	clock *HybridClock
	value T
	ts    HybridTimestamp
}

// NewLWWRegister creates a register owned by node, using clock for
// timestamp generation. The register holds initial as its starting value
// (with a zero timestamp, so any real write will win immediately).
func NewLWWRegister[T any](node NodeID, clock *HybridClock, initial T) *LWWRegister[T] {
	return &LWWRegister[T]{
		Node:  node,
		clock: clock,
		value: initial,
	}
}

// Set writes v with a fresh hybrid timestamp.
func (r *LWWRegister[T]) Set(v T) {
	ts := r.clock.Now(r.Node)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.value = v
	r.ts = ts
}

// Get returns the current value.
func (r *LWWRegister[T]) Get() T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}

// Timestamp returns the HybridTimestamp of the last accepted write.
func (r *LWWRegister[T]) Timestamp() HybridTimestamp {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ts
}

// Merge absorbs the state of another LWWRegister[T]. The incoming value
// wins if its timestamp is After the current one, or if the timestamps are
// Equal and the incoming NodeID is greater (lexicographic tiebreak).
// Merge returns an error if other is nil or not a *LWWRegister[T].
func (r *LWWRegister[T]) Merge(other CRDT) error {
	if other == nil {
		return errors.New("lwwregister: cannot merge nil CRDT")
	}
	o, ok := other.(*LWWRegister[T])
	if !ok {
		return errors.New("lwwregister: type mismatch on Merge")
	}

	// Snapshot the incoming register under its lock.
	o.mu.Lock()
	oVal := o.value
	oTs := o.ts
	o.mu.Unlock()

	// Advance our clock to dominate the incoming timestamp.
	r.clock.Observe(oTs)

	r.mu.Lock()
	defer r.mu.Unlock()
	if oTs.After(r.ts) || (oTs.Equal(r.ts) && oTs.Node > r.ts.Node) {
		r.value = oVal
		r.ts = oTs
	}
	return nil
}

// lwwWire is the JSON envelope for Snapshot/Restore.
type lwwWire struct {
	Node  NodeID          `json:"node"`
	Value json.RawMessage `json:"value"`
	Ts    HybridTimestamp `json:"ts"`
}

// Snapshot serializes the full register state to JSON.
func (r *LWWRegister[T]) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	valBytes, err := json.Marshal(r.value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(lwwWire{Node: r.Node, Value: valBytes, Ts: r.ts})
}

// Restore replaces the register state with the output of a prior Snapshot.
func (r *LWWRegister[T]) Restore(data []byte) error {
	var w lwwWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	var v T
	if err := json.Unmarshal(w.Value, &v); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Node = w.Node
	r.value = v
	r.ts = w.Ts
	return nil
}
