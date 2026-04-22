package crdt

import (
	"testing"
	"time"
)

func TestLWWRegisterLastWriteWins(t *testing.T) {
	clk := &HybridClock{}
	r := NewLWWRegister[string]("nodeA", clk, "initial")

	r.Set("first")
	time.Sleep(time.Nanosecond) // ensure wall-clock advances
	r.Set("second")

	if got := r.Get(); got != "second" {
		t.Fatalf("want %q, got %q", "second", got)
	}
}

func TestLWWRegisterConcurrentTieBreakByNode(t *testing.T) {
	// Create two registers with the same (Wall, Logical) but different
	// NodeIDs. The higher NodeID must win on merge.
	clkA := &HybridClock{}
	clkB := &HybridClock{}
	// Drive both clocks to the same Wall time so their next Now() calls
	// produce Logical=1 on the same wall nanosecond.
	fixedTs := HybridTimestamp{Wall: 2_000_000_000, Logical: 5, Node: "nodeA"}
	clkA.Observe(fixedTs)
	clkB.Observe(HybridTimestamp{Wall: 2_000_000_000, Logical: 5, Node: "nodeZ"})

	rA2 := NewLWWRegister[string]("nodeA", clkA, "valueA")
	rB2 := NewLWWRegister[string]("nodeZ", clkB, "valueZ")
	rA2.Set("valueA")
	rB2.Set("valueZ")

	// Both rA2 and rB2 have ts.Wall==2e9, ts.Logical==6, ts.Node=="nodeA"/"nodeZ".
	// nodeZ > nodeA lexicographically, so rB2's value should win.
	if err := rA2.Merge(rB2); err != nil {
		t.Fatal(err)
	}
	// After merge: nodeZ > nodeA, so "valueZ" wins.
	if got := rA2.Get(); got != "valueZ" {
		t.Fatalf("tie-break: want %q, got %q", "valueZ", got)
	}
}

func TestLWWRegisterRoundTrip(t *testing.T) {
	clk := &HybridClock{}
	r := NewLWWRegister[int]("nodeA", clk, 0)
	r.Set(99)

	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	clk2 := &HybridClock{}
	got := NewLWWRegister[int]("", clk2, 0)
	if err := got.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got.Get() != r.Get() {
		t.Fatalf("round-trip: want %d, got %d", r.Get(), got.Get())
	}
	if !got.Timestamp().Equal(r.Timestamp()) {
		t.Fatalf("round-trip timestamp mismatch")
	}
}

func TestLWWRegisterMergeNilError(t *testing.T) {
	clk := &HybridClock{}
	r := NewLWWRegister[string]("nodeA", clk, "v")
	if err := r.Merge(nil); err == nil {
		t.Fatal("expected error merging nil")
	}
}

func TestLWWRegisterMergeKeepsHigherTimestamp(t *testing.T) {
	clkA := &HybridClock{}
	clkB := &HybridClock{}

	rA := NewLWWRegister[string]("nodeA", clkA, "")
	rB := NewLWWRegister[string]("nodeB", clkB, "")

	rA.Set("old")
	time.Sleep(2 * time.Millisecond)
	rB.Set("new")

	if err := rA.Merge(rB); err != nil {
		t.Fatal(err)
	}
	if got := rA.Get(); got != "new" {
		t.Fatalf("want %q, got %q", "new", got)
	}
}
