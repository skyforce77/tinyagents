package crdt

import (
	"testing"
)

func TestGCounterIncAndValue(t *testing.T) {
	c := NewGCounter("nodeA")
	c.Inc(3)
	c.Inc(7)
	if got := c.Value(); got != 10 {
		t.Fatalf("want 10, got %d", got)
	}
}

func TestGCounterMergeTakesMax(t *testing.T) {
	a := NewGCounter("nodeA")
	b := NewGCounter("nodeB")

	a.Inc(5)
	b.Inc(3)

	// Diverge: a increments its own slot further.
	a.Inc(2) // nodeA=7

	// Merge b into a.
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	// a should have nodeA=7, nodeB=3 → total=10.
	if got := a.Value(); got != 10 {
		t.Fatalf("want 10, got %d", got)
	}

	// Merging again is idempotent.
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if got := a.Value(); got != 10 {
		t.Fatalf("idempotent: want 10, got %d", got)
	}
}

func TestGCounterCommutative(t *testing.T) {
	a := NewGCounter("nodeA")
	b := NewGCounter("nodeB")
	a.Inc(4)
	b.Inc(6)

	// Merge a←b and b←a; both should converge to the same Value.
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a); err != nil {
		t.Fatal(err)
	}
	if a.Value() != b.Value() {
		t.Fatalf("commutative: a=%d b=%d", a.Value(), b.Value())
	}
}

func TestGCounterRestoreRoundTrip(t *testing.T) {
	c := NewGCounter("nodeA")
	c.Inc(42)
	roundTripGCounter(t, c)
}

// roundTripGCounter is a helper shared by this file.
func roundTripGCounter(t *testing.T, c *GCounter) {
	t.Helper()
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := NewGCounter("")
	if err := got.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if c.Value() != got.Value() {
		t.Fatalf("round-trip value mismatch: want %d, got %d", c.Value(), got.Value())
	}
	if c.Node != got.Node {
		t.Fatalf("round-trip node mismatch: want %q, got %q", c.Node, got.Node)
	}
}

func TestGCounterMergeNilError(t *testing.T) {
	c := NewGCounter("nodeA")
	if err := c.Merge(nil); err == nil {
		t.Fatal("expected error merging nil")
	}
}

func TestGCounterMergeTypeMismatch(t *testing.T) {
	c := NewGCounter("nodeA")
	p := NewPNCounter("nodeB")
	if err := c.Merge(p); err == nil {
		t.Fatal("expected error merging wrong type")
	}
}
