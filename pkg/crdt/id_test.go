package crdt

import (
	"testing"
	"time"
)

func TestHybridClockMonotonic(t *testing.T) {
	clk := &HybridClock{}
	const N = 1000
	prev := clk.Now("nodeA")
	for i := 0; i < N; i++ {
		cur := clk.Now("nodeA")
		if !prev.Before(cur) && !prev.Equal(cur) {
			// prev must be strictly before cur
			t.Fatalf("clock not monotonic at iteration %d: prev=%+v cur=%+v", i, prev, cur)
		}
		if cur.Equal(prev) {
			t.Fatalf("clock returned equal timestamp at iteration %d", i)
		}
		prev = cur
	}
}

func TestHybridClockStrictlyIncreasing(t *testing.T) {
	clk := &HybridClock{}
	a := clk.Now("nodeA")
	b := clk.Now("nodeA")
	if !a.Before(b) {
		t.Fatalf("expected a.Before(b): a=%+v b=%+v", a, b)
	}
}

func TestHybridClockObservePullsForward(t *testing.T) {
	clk := &HybridClock{}
	// Observe a timestamp far in the future.
	future := HybridTimestamp{
		Wall:    time.Now().Add(10 * time.Hour).UnixNano(),
		Logical: 42,
		Node:    "remoteNode",
	}
	clk.Observe(future)

	// The next Now() must produce a timestamp after the observed one.
	ts := clk.Now("localNode")
	if !future.Before(ts) && !future.Equal(ts) {
		t.Fatalf("expected ts to dominate future: future=%+v ts=%+v", future, ts)
	}
}

func TestHybridTimestampOrdering(t *testing.T) {
	a := HybridTimestamp{Wall: 100, Logical: 0, Node: "A"}
	b := HybridTimestamp{Wall: 200, Logical: 0, Node: "A"}
	c := HybridTimestamp{Wall: 200, Logical: 1, Node: "A"}
	d := HybridTimestamp{Wall: 200, Logical: 1, Node: "B"}

	if !a.Before(b) {
		t.Error("a should be before b (wall time)")
	}
	if !b.Before(c) {
		t.Error("b should be before c (logical counter)")
	}
	if !c.Before(d) {
		t.Error("c should be before d (node tiebreak)")
	}
	if a.After(b) {
		t.Error("a should not be after b")
	}
	if !a.Equal(a) {
		t.Error("a should equal itself")
	}
}
