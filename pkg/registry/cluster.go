// Package registry exposes a read-only view of a running actor System.
// This file adds ClusterRegistry: a consistent-hash ring over the current
// cluster membership that answers "which node owns this actor path?" in
// O(log N) and rebuilds on every membership event.
package registry

import (
	"fmt"
	"hash/fnv"
	"sort"
	"sync"

	"github.com/skyforce77/tinyagents/pkg/cluster"
)

// ringEntry is one slot on the 64-bit consistent-hash ring.
type ringEntry struct {
	hash uint64
	node string
}

// ClusterRegistry answers "which node owns this path?" using consistent
// hashing over the current cluster membership. It keeps the local
// Registry as its in-process cache and re-computes the hash ring on
// every cluster Event.
type ClusterRegistry struct {
	local    Registry
	cl       cluster.Cluster
	replicas int

	closeOnce   sync.Once
	unsubscribe func() // set once in constructor; called at most once via closeOnce

	mu   sync.RWMutex
	ring []ringEntry // sorted by hash, rebuilt on membership change
}

// NewClusterRegistry builds a ClusterRegistry. replicas controls the number
// of virtual nodes per member on the ring (128 is a reasonable default
// balancing ring size vs. load smoothness). Zero falls back to 128.
// The ClusterRegistry subscribes to cluster events in its constructor
// and unsubscribes in Close.
func NewClusterRegistry(local Registry, cl cluster.Cluster, replicas int) *ClusterRegistry {
	if replicas <= 0 {
		replicas = 128
	}

	c := &ClusterRegistry{
		local:    local,
		cl:       cl,
		replicas: replicas,
	}

	// Seed the ring from the current membership snapshot so that OwnerOf
	// works immediately after construction, even before any events fire.
	c.rebuildLocked(cl.Members())

	c.unsubscribe = cl.Subscribe(func(ev cluster.Event) {
		switch ev.Kind {
		case cluster.MemberJoined, cluster.MemberLeft, cluster.MemberUpdated:
			c.mu.Lock()
			c.rebuildLocked(c.cl.Members())
			c.mu.Unlock()
		}
	})

	return c
}

// Local returns the embedded in-process Registry — convenient when callers
// need to register or look up local Refs directly.
func (c *ClusterRegistry) Local() Registry {
	return c.local
}

// OwnerOf returns the nodeID responsible for the given actor path.
// It never returns an empty string once Join has completed; before
// Join, it returns the local node's ID (single-node fallback).
func (c *ClusterRegistry) OwnerOf(path string) string {
	c.mu.RLock()
	ring := c.ring
	c.mu.RUnlock()

	if len(ring) == 0 {
		// No members yet — fall back to the local node.
		return c.cl.LocalNode().ID
	}

	h := fnvHash(path)

	// Binary search for the first entry whose hash >= h.
	i := sort.Search(len(ring), func(i int) bool {
		return ring[i].hash >= h
	})

	// Wrap around to the first entry if we went past the end.
	if i >= len(ring) {
		i = 0
	}

	return ring[i].node
}

// IsLocal reports whether the local node currently owns path.
func (c *ClusterRegistry) IsLocal(path string) bool {
	return c.OwnerOf(path) == c.cl.LocalNode().ID
}

// Close unsubscribes from cluster events. Safe to call multiple times.
func (c *ClusterRegistry) Close() error {
	c.closeOnce.Do(c.unsubscribe)
	return nil
}

// rebuildLocked replaces the ring with a fresh snapshot derived from members.
// Must be called with c.mu held for writing (or during single-threaded
// construction before the ClusterRegistry is shared).
func (c *ClusterRegistry) rebuildLocked(members []cluster.Member) {
	c.ring = buildRing(members, c.replicas)
}

// buildRing constructs a sorted []ringEntry for the given members.
func buildRing(members []cluster.Member, replicas int) []ringEntry {
	ring := make([]ringEntry, 0, len(members)*replicas)
	for _, m := range members {
		for i := 0; i < replicas; i++ {
			key := fmt.Sprintf("%s#%d", m.ID, i)
			ring = append(ring, ringEntry{
				hash: fnvHash(key),
				node: m.ID,
			})
		}
	}
	sort.Slice(ring, func(i, j int) bool {
		return ring[i].hash < ring[j].hash
	})
	return ring
}

// fnvHash computes the FNV-64a hash of s.
func fnvHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
