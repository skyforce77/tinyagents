package crdt

import (
	"reflect"
	"testing"
)

func TestORSetAddRemove(t *testing.T) {
	s := NewORSet[string]("nodeA")

	s.Add("apple")
	if !s.Contains("apple") {
		t.Fatal("apple should be present after Add")
	}

	s.Remove("apple")
	if s.Contains("apple") {
		t.Fatal("apple should be absent after Remove")
	}

	// Add again after remove should make it present.
	s.Add("apple")
	if !s.Contains("apple") {
		t.Fatal("apple should be present after second Add")
	}
}

func TestORSetConvergence(t *testing.T) {
	a := NewORSet[string]("nodeA")
	b := NewORSet[string]("nodeB")

	// Both nodes independently add elements.
	a.Add("x")
	a.Add("y")
	b.Add("y")
	b.Add("z")

	// nodeA removes "y" without knowing about nodeB's add of "y".
	a.Remove("y")

	// Merge both directions.
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a); err != nil {
		t.Fatal(err)
	}

	// After convergence both replicas must agree.
	aVals := a.Values()
	bVals := b.Values()
	if !reflect.DeepEqual(aVals, bVals) {
		t.Fatalf("convergence: a=%v b=%v", aVals, bVals)
	}
}

func TestORSetAddWinsOverRemove(t *testing.T) {
	// Classic add-wins scenario:
	// nodeA removes "item"; nodeB concurrently adds "item".
	// After merge, "item" should be present.
	a := NewORSet[string]("nodeA")
	b := NewORSet[string]("nodeB")

	// Both start knowing about "item".
	a.Add("item")
	if err := b.Merge(a); err != nil {
		t.Fatal(err)
	}

	// nodeA removes "item"; nodeB adds "item" again (concurrent).
	a.Remove("item")
	b.Add("item") // b adds a new tag that a hasn't seen

	// Merge.
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}

	// Add wins: b's fresh tag survives a's tombstone.
	if !a.Contains("item") {
		t.Fatal("add-wins: item should be present after merge")
	}
}

func TestORSetRoundTrip(t *testing.T) {
	s := NewORSet[string]("nodeA")
	s.Add("alpha")
	s.Add("beta")
	s.Remove("alpha")

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	got := NewORSet[string]("")
	if err := got.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if !reflect.DeepEqual(s.Values(), got.Values()) {
		t.Fatalf("round-trip values: want %v, got %v", s.Values(), got.Values())
	}
	if s.Contains("alpha") != got.Contains("alpha") {
		t.Fatalf("round-trip Contains mismatch for alpha")
	}
	if s.Contains("beta") != got.Contains("beta") {
		t.Fatalf("round-trip Contains mismatch for beta")
	}
}

func TestORSetMergeNilError(t *testing.T) {
	s := NewORSet[string]("nodeA")
	if err := s.Merge(nil); err == nil {
		t.Fatal("expected error merging nil")
	}
}

func TestORSetMergeTypeMismatch(t *testing.T) {
	s := NewORSet[string]("nodeA")
	g := NewGCounter("nodeB")
	if err := s.Merge(g); err == nil {
		t.Fatal("expected error merging wrong type")
	}
}
