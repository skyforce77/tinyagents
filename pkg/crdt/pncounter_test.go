package crdt

import (
	"testing"
)

func TestPNCounterIncDec(t *testing.T) {
	c := NewPNCounter("nodeA")
	c.Inc(10)
	c.Dec(3)
	if got := c.Value(); got != 7 {
		t.Fatalf("want 7, got %d", got)
	}
}

func TestPNCounterMergeConverges(t *testing.T) {
	a := NewPNCounter("nodeA")
	b := NewPNCounter("nodeB")

	a.Inc(10)
	b.Inc(5)
	a.Dec(2)
	b.Dec(1)

	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a); err != nil {
		t.Fatal(err)
	}
	// Both replicas must converge to the same value.
	if a.Value() != b.Value() {
		t.Fatalf("convergence: a=%d b=%d", a.Value(), b.Value())
	}
	// Expected: (10+5) - (2+1) = 12
	if a.Value() != 12 {
		t.Fatalf("want 12, got %d", a.Value())
	}
}

func TestPNCounterCommutative(t *testing.T) {
	a := NewPNCounter("nodeA")
	b := NewPNCounter("nodeB")
	a.Inc(8)
	b.Dec(3)

	// Merge in both directions.
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

func TestPNCounterRestoreRoundTrip(t *testing.T) {
	c := NewPNCounter("nodeA")
	c.Inc(20)
	c.Dec(7)
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := NewPNCounter("")
	if err := got.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if c.Value() != got.Value() {
		t.Fatalf("round-trip: want %d, got %d", c.Value(), got.Value())
	}
}

func TestPNCounterMergeNilError(t *testing.T) {
	c := NewPNCounter("nodeA")
	if err := c.Merge(nil); err == nil {
		t.Fatal("expected error merging nil")
	}
}

func TestPNCounterMergeTypeMismatch(t *testing.T) {
	c := NewPNCounter("nodeA")
	g := NewGCounter("nodeB")
	if err := c.Merge(g); err == nil {
		t.Fatal("expected error merging wrong type")
	}
}
