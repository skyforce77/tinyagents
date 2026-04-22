package registry_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/cluster"
	"github.com/skyforce77/tinyagents/pkg/registry"
)

// ---------------------------------------------------------------------------
// fakeCluster — a cluster.Cluster backed by a manually-controlled member list.
// Events are dispatched synchronously to handlers when fireEvent is called,
// which avoids goroutine-timing hazards in unit tests.
// ---------------------------------------------------------------------------

type fakeCluster struct {
	mu      sync.Mutex
	self    cluster.Member
	members []cluster.Member
	subs    map[uint64]cluster.EventHandler
	nextID  uint64
}

func newFakeCluster(selfID string, initial []cluster.Member) *fakeCluster {
	return &fakeCluster{
		self:    cluster.Member{ID: selfID},
		members: append([]cluster.Member{}, initial...),
		subs:    make(map[uint64]cluster.EventHandler),
	}
}

func (f *fakeCluster) Join(_ context.Context, _ ...string) error { return nil }
func (f *fakeCluster) Leave(_ context.Context) error             { return nil }

func (f *fakeCluster) Members() []cluster.Member {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cluster.Member, len(f.members))
	copy(out, f.members)
	return out
}

func (f *fakeCluster) LocalNode() cluster.Member {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.self
}

// Subscribe stores h; the returned function removes it. Events are delivered
// synchronously from fireEvent — no goroutine, no channel.
func (f *fakeCluster) Subscribe(h cluster.EventHandler) (unsubscribe func()) {
	f.mu.Lock()
	id := f.nextID
	f.nextID++
	f.subs[id] = h
	f.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			delete(f.subs, id)
			f.mu.Unlock()
		})
	}
}

func (f *fakeCluster) Close() error { return nil }

// fireEvent updates the member list and calls every registered handler
// synchronously before returning. After fireEvent returns the ClusterRegistry's
// ring has already been rebuilt.
func (f *fakeCluster) fireEvent(ev cluster.Event) {
	f.mu.Lock()
	// Update member list.
	switch ev.Kind {
	case cluster.MemberJoined:
		f.members = append(f.members, ev.Member)
	case cluster.MemberLeft:
		updated := f.members[:0]
		for _, m := range f.members {
			if m.ID != ev.Member.ID {
				updated = append(updated, m)
			}
		}
		f.members = updated
	}
	// Snapshot handlers.
	handlers := make([]cluster.EventHandler, 0, len(f.subs))
	for _, h := range f.subs {
		handlers = append(handlers, h)
	}
	f.mu.Unlock()

	// Deliver synchronously. The ClusterRegistry handler holds a write lock
	// for the duration of the rebuild — that is safe because no other goroutine
	// in these tests is concurrently calling OwnerOf during the fire.
	for _, h := range handlers {
		h(ev)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestSystem returns a minimal actor.System that satisfies registry.Registry.
// It is stopped at test cleanup.
func newTestSystem(t *testing.T) registry.Registry {
	t.Helper()
	sys := actor.NewSystem("test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = sys.Stop(ctx)
	})
	return sys
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestClusterRegistrySingleNodeOwnsEverything(t *testing.T) {
	t.Parallel()

	fc := newFakeCluster("n1", []cluster.Member{{ID: "n1"}})
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	owner := cr.OwnerOf("anything")
	if owner != "n1" {
		t.Fatalf("OwnerOf = %q, want %q", owner, "n1")
	}
	if !cr.IsLocal("anything") {
		t.Fatal("IsLocal should be true for single-node cluster")
	}
}

func TestClusterRegistryMultiNodeStableMapping(t *testing.T) {
	t.Parallel()

	members := []cluster.Member{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}}
	fc := newFakeCluster("n1", members)
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	const path = "/user/agent-1"
	first := cr.OwnerOf(path)
	if first == "" {
		t.Fatal("OwnerOf returned empty string")
	}
	for i := 0; i < 100; i++ {
		got := cr.OwnerOf(path)
		if got != first {
			t.Fatalf("iteration %d: OwnerOf = %q, want %q", i, got, first)
		}
	}
}

func TestClusterRegistryLoadBalance(t *testing.T) {
	t.Parallel()

	// Node IDs chosen so that FNV-64a + 128 replicas produces well-spread
	// ring entries. Very similar IDs (e.g. "n1"/"n2"/"n3") can cluster on
	// the ring due to FNV's avalanche properties on near-identical strings.
	members := []cluster.Member{
		{ID: "node-alpha"},
		{ID: "node-beta"},
		{ID: "node-gamma"},
	}
	fc := newFakeCluster("node-alpha", members)
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	const total = 10_000
	counts := map[string]int{}
	for i := 0; i < total; i++ {
		owner := cr.OwnerOf(fmt.Sprintf("/user/agent-%d", i))
		counts[owner]++
	}

	for _, m := range members {
		c := counts[m.ID]
		if c < 2500 || c > 4500 {
			t.Errorf("node %q owns %d/%d paths, want [2500, 4500]; full distribution: %v",
				m.ID, c, total, counts)
		}
	}
}

func TestClusterRegistryJoinRebalances(t *testing.T) {
	t.Parallel()

	members := []cluster.Member{{ID: "n1"}, {ID: "n2"}}
	fc := newFakeCluster("n1", members)
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	// Capture ownership before the new node joins.
	const total = 1000
	before := make([]string, total)
	for i := 0; i < total; i++ {
		before[i] = cr.OwnerOf(fmt.Sprintf("/user/a-%d", i))
	}

	// Fire MemberJoined for a third node.
	fc.fireEvent(cluster.Event{
		Kind:   cluster.MemberJoined,
		Member: cluster.Member{ID: "n3"},
	})

	// Some paths must now map to n3.
	n3Count := 0
	for i := 0; i < total; i++ {
		if cr.OwnerOf(fmt.Sprintf("/user/a-%d", i)) == "n3" {
			n3Count++
		}
	}
	if n3Count == 0 {
		t.Fatal("after joining n3, zero paths map to it — ring was not rebuilt")
	}
	t.Logf("after n3 joins, %d/%d paths map to n3", n3Count, total)
}

func TestClusterRegistryLeaveRebalances(t *testing.T) {
	t.Parallel()

	members := []cluster.Member{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}}
	fc := newFakeCluster("n1", members)
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	// Fire MemberLeft for n2.
	fc.fireEvent(cluster.Event{
		Kind:   cluster.MemberLeft,
		Member: cluster.Member{ID: "n2"},
	})

	// n2 must never be returned as owner.
	for i := 0; i < 1000; i++ {
		owner := cr.OwnerOf(fmt.Sprintf("/user/b-%d", i))
		if owner == "n2" {
			t.Fatalf("path /user/b-%d maps to departed node n2", i)
		}
	}
}

func TestClusterRegistryCloseUnsubscribes(t *testing.T) {
	t.Parallel()

	members := []cluster.Member{{ID: "n1"}, {ID: "n2"}}
	fc := newFakeCluster("n1", members)
	cr := registry.NewClusterRegistry(newTestSystem(t), fc, 128)

	// Close first.
	if err := cr.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	// Second Close must be idempotent.
	if err := cr.Close(); err != nil {
		t.Fatalf("second Close error: %v", err)
	}

	// Fire an event after Close — must not panic.
	fc.fireEvent(cluster.Event{
		Kind:   cluster.MemberJoined,
		Member: cluster.Member{ID: "n3"},
	})

	// OwnerOf must not panic; ring was built before Close so it still works.
	_ = cr.OwnerOf("/user/any")
}

func TestClusterRegistryLocalReturnsEmbedded(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	fc := newFakeCluster("n1", []cluster.Member{{ID: "n1"}})
	cr := registry.NewClusterRegistry(sys, fc, 128)
	t.Cleanup(func() { _ = cr.Close() })

	if cr.Local() != sys {
		t.Fatal("Local() did not return the Registry passed to NewClusterRegistry")
	}
}
