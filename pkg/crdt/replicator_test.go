package crdt

import (
	"context"
	"sync"
	"testing"
	"time"
)

func init() {
	// Register generic types used in replicator tests.
	RegisterType("lww[string]", func(id string) CRDT {
		return NewLWWRegister[string](NodeID(id), &HybridClock{}, "")
	})
	RegisterType("orset[string]", func(id string) CRDT {
		return NewORSet[string](NodeID(id))
	})
}

// bus is an in-process broadcast primitive that fans out payloads to every
// registered Replicator except the sender (identified by node ID).
type bus struct {
	mu   sync.RWMutex
	reps map[string]*Replicator
}

func newBus() *bus { return &bus{reps: make(map[string]*Replicator)} }

// add registers a Replicator with the bus under its node ID.
func (b *bus) add(id string, r *Replicator) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reps[id] = r
}

// broadcastFor returns a Broadcast function whose sender identity is senderID.
// The returned function delivers to all other replicators registered on the bus.
func (b *bus) broadcastFor(senderID string) Broadcast {
	return func(payload []byte) error {
		b.mu.RLock()
		peers := make([]*Replicator, 0, len(b.reps))
		for id, r := range b.reps {
			if id != senderID {
				peers = append(peers, r)
			}
		}
		b.mu.RUnlock()
		for _, p := range peers {
			if err := p.Deliver(payload); err != nil {
				return err
			}
		}
		return nil
	}
}

// TestReplicatorGCounterConverges verifies that 3 replicas each increment
// a GCounter locally then Replicate; after delivery every replica observes
// the global sum.
func TestReplicatorGCounterConverges(t *testing.T) {
	b := newBus()

	nodes := []string{"n1", "n2", "n3"}
	reps := make([]*Replicator, len(nodes))
	counters := make([]*GCounter, len(nodes))

	for i, id := range nodes {
		counters[i] = NewGCounter(NodeID(id))
		reps[i] = New(id, b.broadcastFor(id))
		reps[i].Register("tokens", counters[i], "gcounter")
		b.add(id, reps[i])
	}

	// Each node increments by its 1-based index.
	for i := range nodes {
		counters[i].Inc(uint64(i + 1)) // 1, 2, 3 → sum = 6
		if err := reps[i].Replicate("tokens"); err != nil {
			t.Fatalf("node %s Replicate: %v", nodes[i], err)
		}
	}

	// All replicas must converge to 6.
	want := uint64(6)
	for i, c := range counters {
		if got := c.Value(); got != want {
			t.Errorf("node %s: want %d, got %d", nodes[i], want, got)
		}
	}
}

// TestReplicatorPNCounterConverges verifies convergence for Inc + Dec across
// 3 replicas.
func TestReplicatorPNCounterConverges(t *testing.T) {
	b := newBus()

	nodes := []string{"n1", "n2", "n3"}
	reps := make([]*Replicator, len(nodes))
	counters := make([]*PNCounter, len(nodes))

	for i, id := range nodes {
		counters[i] = NewPNCounter(NodeID(id))
		reps[i] = New(id, b.broadcastFor(id))
		reps[i].Register("score", counters[i], "pncounter")
		b.add(id, reps[i])
	}

	// n1: +10, n2: +5 −3, n3: −2 → net = 10 + 5 - 3 - 2 = 10
	counters[0].Inc(10)
	if err := reps[0].Replicate("score"); err != nil {
		t.Fatal(err)
	}

	counters[1].Inc(5)
	counters[1].Dec(3)
	if err := reps[1].Replicate("score"); err != nil {
		t.Fatal(err)
	}

	counters[2].Dec(2)
	if err := reps[2].Replicate("score"); err != nil {
		t.Fatal(err)
	}

	want := int64(10)
	for i, c := range counters {
		if got := c.Value(); got != want {
			t.Errorf("node %s: want %d, got %d", nodes[i], want, got)
		}
	}
}

// TestReplicatorLWWConverges verifies that after overlapping Set calls on 3
// replicas the one with the greatest timestamp wins everywhere.
func TestReplicatorLWWConverges(t *testing.T) {
	b := newBus()

	nodes := []string{"n1", "n2", "n3"}
	reps := make([]*Replicator, len(nodes))
	registers := make([]*LWWRegister[string], len(nodes))
	clocks := make([]*HybridClock, len(nodes))

	for i, id := range nodes {
		clocks[i] = &HybridClock{}
		registers[i] = NewLWWRegister[string](NodeID(id), clocks[i], "")
		reps[i] = New(id, b.broadcastFor(id))
		reps[i].Register("label", registers[i], "lww[string]")
		b.add(id, reps[i])
	}

	// Set up clocks so that n2's write wins. We use future wall-clock values
	// that are guaranteed to be greater than the real clock (year ~2096 and
	// ~2097 in nanoseconds). Each clock is advanced to fixedHigh before Set
	// so Now() will bump the logical counter and stay within the same wall
	// nanosecond — but n2 gets a higher wall value than n1 and n3.
	//
	// n1: wall = fixedA, logical = 1
	// n2: wall = fixedB (> fixedA), logical = 1 → wins
	// n3: wall = fixedA, logical = 1
	now := time.Now().UnixNano()
	fixedA := HybridTimestamp{Wall: now + 4_000_000_000_000_000_000, Logical: 0}
	fixedB := HybridTimestamp{Wall: now + 5_000_000_000_000_000_000, Logical: 0}

	clocks[0].Observe(fixedA)
	registers[0].Set("value-A") // ts.Wall = fixedA.Wall

	clocks[1].Observe(fixedB)
	registers[1].Set("value-B") // ts.Wall = fixedB.Wall → greatest

	clocks[2].Observe(fixedA)
	registers[2].Set("value-C") // ts.Wall = fixedA.Wall

	// Replicate all.
	for i := range nodes {
		if err := reps[i].Replicate("label"); err != nil {
			t.Fatalf("node %s Replicate: %v", nodes[i], err)
		}
	}

	want := "value-B"
	for i, reg := range registers {
		if got := reg.Get(); got != want {
			t.Errorf("node %s: want %q, got %q", nodes[i], want, got)
		}
	}
}

// TestReplicatorORSetConverges verifies add-wins convergence for ORSet across
// 3 replicas including a Remove.
func TestReplicatorORSetConverges(t *testing.T) {
	b := newBus()

	nodes := []string{"n1", "n2", "n3"}
	reps := make([]*Replicator, len(nodes))
	sets := make([]*ORSet[string], len(nodes))

	for i, id := range nodes {
		sets[i] = NewORSet[string](NodeID(id))
		reps[i] = New(id, b.broadcastFor(id))
		reps[i].Register("members", sets[i], "orset[string]")
		b.add(id, reps[i])
	}

	// n1 adds "alice", n2 adds "bob", n3 adds "carol" then removes "carol".
	sets[0].Add("alice")
	if err := reps[0].Replicate("members"); err != nil {
		t.Fatal(err)
	}

	sets[1].Add("bob")
	if err := reps[1].Replicate("members"); err != nil {
		t.Fatal(err)
	}

	sets[2].Add("carol")
	sets[2].Remove("carol")
	if err := reps[2].Replicate("members"); err != nil {
		t.Fatal(err)
	}

	// All should have {"alice", "bob"}.
	for i, s := range sets {
		got := s.Values()
		if len(got) != 2 {
			t.Errorf("node %s: want 2 elements, got %v", nodes[i], got)
			continue
		}
		m := make(map[string]bool, 2)
		for _, v := range got {
			m[v] = true
		}
		if !m["alice"] || !m["bob"] {
			t.Errorf("node %s: want {alice,bob}, got %v", nodes[i], got)
		}
	}
}

// TestReplicatorIgnoresOwnEcho verifies that a Replicator whose payload is
// looped back via Deliver is not double-counted.
func TestReplicatorIgnoresOwnEcho(t *testing.T) {
	// Use a pointer-to-Replicator so the closure can close over it before
	// New() returns.
	var rp *Replicator
	rp = New("solo", func(payload []byte) error {
		// Echo the payload back to the same replicator.
		return rp.Deliver(payload)
	})

	gc := NewGCounter("solo")
	rp.Register("c", gc, "gcounter")

	gc.Inc(5)
	if err := rp.Replicate("c"); err != nil {
		t.Fatal(err)
	}

	if got := gc.Value(); got != 5 {
		t.Fatalf("want 5 (no double-count), got %d", got)
	}
}

// TestReplicatorUnknownKeyDropped verifies that a Deliver for an unregistered
// key returns nil (not an error) and logs a warn.
func TestReplicatorUnknownKeyDropped(t *testing.T) {
	sender := New("sender", func([]byte) error { return nil })
	gc := NewGCounter("sender")
	sender.Register("c", gc, "gcounter")

	// Build a raw payload for key "c" from sender.
	gc.Inc(1)
	var captured []byte
	sender2 := New("sender", func(p []byte) error {
		captured = p
		return nil
	})
	gc2 := NewGCounter("sender")
	sender2.Register("c", gc2, "gcounter")
	gc2.Inc(1)
	if err := sender2.Replicate("c"); err != nil {
		t.Fatal(err)
	}

	// A replicator with no registered keys.
	receiver := New("receiver", func([]byte) error { return nil })
	if err := receiver.Deliver(captured); err != nil {
		t.Fatalf("expected nil error for unknown key, got: %v", err)
	}
}

// TestReplicatorSyncIntervalRepublishes verifies that a late-joining peer
// catches up via the periodic anti-entropy broadcast.
func TestReplicatorSyncIntervalRepublishes(t *testing.T) {
	const interval = 50 * time.Millisecond

	b := newBus()

	// Node A starts with state.
	gcA := NewGCounter("nodeA")
	repA := New("nodeA", b.broadcastFor("nodeA"), WithSyncInterval(interval))
	repA.Register("counter", gcA, "gcounter")
	b.add("nodeA", repA)

	gcA.Inc(42)
	// Replicate before B is on the bus — B misses this.
	if err := repA.Replicate("counter"); err != nil {
		t.Fatal(err)
	}

	// Node B joins late.
	gcB := NewGCounter("nodeB")
	repB := New("nodeB", b.broadcastFor("nodeB"))
	repB.Register("counter", gcB, "gcounter")
	b.add("nodeB", repB)

	// Start periodic sync on A; B should catch up within a few intervals.
	ctx, cancel := context.WithTimeout(context.Background(), 10*interval)
	defer cancel()
	repA.Start(ctx)

	// Poll until B converges or the context expires.
	deadline := time.Now().Add(8 * interval)
	for time.Now().Before(deadline) {
		if gcB.Value() == 42 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := gcB.Value(); got != 42 {
		t.Fatalf("late joiner: want 42, got %d", got)
	}

	repA.Close()
	repB.Close()
}

// TestReplicatorCloseStopsSync verifies that Close cancels the sync goroutine
// without leaking it (checked by the race detector and go test -timeout).
func TestReplicatorCloseStopsSync(t *testing.T) {
	const interval = 20 * time.Millisecond

	gc := NewGCounter("solo")
	rep := New("solo", func([]byte) error { return nil }, WithSyncInterval(interval))
	rep.Register("c", gc, "gcounter")

	ctx := context.Background()
	rep.Start(ctx)

	// Let the goroutine tick a couple of times.
	time.Sleep(3 * interval)

	// Close must be idempotent and stop the goroutine.
	if err := rep.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rep.Close(); err != nil {
		t.Fatal(err)
	}

	// Give the goroutine time to exit; if it doesn't the -race or -timeout
	// flags will catch it.
	time.Sleep(2 * interval)
}
