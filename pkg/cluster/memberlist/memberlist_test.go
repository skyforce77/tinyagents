package memberlist_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	ml "github.com/hashicorp/memberlist"

	"github.com/skyforce77/tinyagents/pkg/cluster"
	mlcluster "github.com/skyforce77/tinyagents/pkg/cluster/memberlist"
)

// newLocalCluster creates a Cluster bound to 127.0.0.1 on a random ephemeral
// port. It uses DefaultLocalConfig so nodes can discover each other in-process
// without real UDP traffic on the LAN.
func newLocalCluster(t *testing.T, id string, opts ...mlcluster.Option) *mlcluster.Cluster {
	t.Helper()

	// Build a memberlist config first to grab an ephemeral port — we pass
	// bind as "127.0.0.1:0" to let the OS pick.
	//
	// NewLocalCluster uses a fully in-process transport so we can spin up
	// multiple nodes without real network sockets.
	cfg := ml.DefaultLocalConfig()
	cfg.BindAddr = "127.0.0.1"
	cfg.BindPort = 0
	cfg.Name = id

	// Silence memberlist's own logger.
	cfg.LogOutput = noopWriter{}
	cfg.Logger = nil

	// Allocate a raw memberlist to discover the chosen port, then shut it
	// down. We only need it to find a free port.
	//
	// Alternatively, we build our Cluster directly via New — but New uses
	// DefaultLANConfig and BindPort 0.  The LocalConfig uses an in-process
	// transport that is much faster for tests.
	//
	// Build using New with bind="127.0.0.1:0".
	c, err := mlcluster.New(id, "127.0.0.1:0", opts...)
	if err != nil {
		t.Fatalf("New(%q): %v", id, err)
	}
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

// noopWriter discards all output.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// pollUntil calls check repeatedly until it returns true or deadline is reached.
func pollUntil(t *testing.T, deadline time.Duration, check func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("pollUntil: deadline exceeded")
}

// seedAddr returns the gossip address of a Cluster so another node can join.
func seedAddr(c *mlcluster.Cluster) string {
	return c.LocalNode().Address
}

// TestMemberlistJoinTwoNodes brings up node A and node B, has B join A as seed,
// and asserts that both sides eventually see 2 members.
func TestMemberlistJoinTwoNodes(t *testing.T) {
	nodeA := newLocalCluster(t, "node-a")
	nodeB := newLocalCluster(t, "node-b")

	ctx := context.Background()
	if err := nodeA.Join(ctx); err != nil {
		t.Fatalf("nodeA.Join: %v", err)
	}
	if err := nodeB.Join(ctx, seedAddr(nodeA)); err != nil {
		t.Fatalf("nodeB.Join: %v", err)
	}

	pollUntil(t, 2*time.Second, func() bool {
		return len(nodeA.Members()) == 2 && len(nodeB.Members()) == 2
	})

	if n := len(nodeA.Members()); n != 2 {
		t.Errorf("nodeA: expected 2 members, got %d", n)
	}
	if n := len(nodeB.Members()); n != 2 {
		t.Errorf("nodeB: expected 2 members, got %d", n)
	}
}

// TestMemberlistEventsOnJoin subscribes on A before B joins and asserts
// that a MemberJoined event for B fires within 2s.
func TestMemberlistEventsOnJoin(t *testing.T) {
	nodeA := newLocalCluster(t, "node-a-ev")
	nodeB := newLocalCluster(t, "node-b-ev")

	ctx := context.Background()
	if err := nodeA.Join(ctx); err != nil {
		t.Fatalf("nodeA.Join: %v", err)
	}

	evCh := make(chan cluster.Event, 16)
	_ = nodeA.Subscribe(func(ev cluster.Event) {
		evCh <- ev
	})

	if err := nodeB.Join(ctx, seedAddr(nodeA)); err != nil {
		t.Fatalf("nodeB.Join: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.Kind == cluster.MemberJoined && ev.Member.ID == "node-b-ev" {
				return // success
			}
		case <-deadline:
			t.Fatal("timeout waiting for MemberJoined event for node-b-ev")
		}
	}
}

// TestMemberlistMetaPropagates sets WithMeta on node B and asserts that after
// joining, node A can see the meta via Members().
func TestMemberlistMetaPropagates(t *testing.T) {
	nodeA := newLocalCluster(t, "node-a-meta")
	nodeB := newLocalCluster(t, "node-b-meta", mlcluster.WithMeta(map[string]string{"role": "worker"}))

	ctx := context.Background()
	if err := nodeA.Join(ctx); err != nil {
		t.Fatalf("nodeA.Join: %v", err)
	}
	if err := nodeB.Join(ctx, seedAddr(nodeA)); err != nil {
		t.Fatalf("nodeB.Join: %v", err)
	}

	// Wait until A sees both nodes with meta propagated.
	pollUntil(t, 2*time.Second, func() bool {
		for _, m := range nodeA.Members() {
			if m.ID == "node-b-meta" && m.Meta["role"] == "worker" {
				return true
			}
		}
		return false
	})

	found := false
	for _, m := range nodeA.Members() {
		if m.ID == "node-b-meta" {
			found = true
			if m.Meta["role"] != "worker" {
				t.Errorf("expected meta role=worker on node-b-meta, got %v", m.Meta)
			}
		}
	}
	if !found {
		t.Error("node-b-meta not found in nodeA.Members()")
	}
}

// TestMemberlistLeaveFiresLeft subscribes on A, has B leave, and asserts that
// a MemberLeft event for B fires within 2s.
func TestMemberlistLeaveFiresLeft(t *testing.T) {
	nodeA := newLocalCluster(t, "node-a-leave")
	nodeB := newLocalCluster(t, "node-b-leave")

	ctx := context.Background()
	if err := nodeA.Join(ctx); err != nil {
		t.Fatalf("nodeA.Join: %v", err)
	}
	if err := nodeB.Join(ctx, seedAddr(nodeA)); err != nil {
		t.Fatalf("nodeB.Join: %v", err)
	}

	// Wait until A sees B.
	pollUntil(t, 2*time.Second, func() bool {
		return len(nodeA.Members()) == 2
	})

	evCh := make(chan cluster.Event, 16)
	_ = nodeA.Subscribe(func(ev cluster.Event) {
		evCh <- ev
	})

	if err := nodeB.Leave(ctx); err != nil {
		t.Fatalf("nodeB.Leave: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.Kind == cluster.MemberLeft && ev.Member.ID == "node-b-leave" {
				return // success
			}
		case <-deadline:
			t.Fatal("timeout waiting for MemberLeft event for node-b-leave")
		}
	}
}

// TestMemberlistCloseIsIdempotent calls Close twice and asserts the second
// call is a no-op (does not panic or return an error).
func TestMemberlistCloseIsIdempotent(t *testing.T) {
	c, err := mlcluster.New("node-close", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestMemberlistSeedlessBootstrap verifies that Join with no seeds succeeds
// and that the cluster has exactly one member (the local node).
func TestMemberlistSeedlessBootstrap(t *testing.T) {
	c, err := mlcluster.New("node-solo", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	if err := c.Join(ctx); err != nil {
		t.Fatalf("Join with no seeds: %v", err)
	}

	members := c.Members()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d: %v", len(members), membersStr(members))
	}
	if members[0].ID != "node-solo" {
		t.Errorf("expected ID node-solo, got %q", members[0].ID)
	}
}

// membersStr formats a []cluster.Member for test failure messages.
func membersStr(ms []cluster.Member) string {
	var s string
	for _, m := range ms {
		s += fmt.Sprintf("{ID:%q Addr:%q} ", m.ID, m.Address)
	}
	return s
}
