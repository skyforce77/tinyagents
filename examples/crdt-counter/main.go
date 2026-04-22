// crdt-counter demonstrates CRDT convergence on three PNCounter replicas
// sharing an in-process bus. The example isolates CRDT semantics from transport.
package main

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/skyforce77/tinyagents/pkg/crdt"
)

// bus is an in-process broadcast primitive that fans out payloads to every
// registered Replicator except the sender (identified by node ID).
type bus struct {
	mu    sync.RWMutex
	peers map[string]*crdt.Replicator
}

func newBus() *bus { return &bus{peers: make(map[string]*crdt.Replicator)} }

// add registers a Replicator with the bus under its node ID.
func (b *bus) add(id string, r *crdt.Replicator) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.peers[id] = r
}

// send returns a Broadcast function whose sender identity is senderID.
// The returned function delivers to all other replicators registered on the bus.
func (b *bus) send(senderID string) crdt.Broadcast {
	return func(payload []byte) error {
		b.mu.RLock()
		peers := make([]*crdt.Replicator, 0, len(b.peers))
		for id, r := range b.peers {
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

func main() {
	ops := flag.Int("ops", 30, "total increment operations spread across nodes")
	decs := flag.Int("decs", 10, "total decrement operations spread across nodes")
	flag.Parse()

	// Create the in-process bus.
	b := newBus()

	// Create three nodes with PNCounter replicas.
	nodeIDs := []string{"n1", "n2", "n3"}
	replicators := make([]*crdt.Replicator, len(nodeIDs))
	counters := make([]*crdt.PNCounter, len(nodeIDs))

	for i, id := range nodeIDs {
		counters[i] = crdt.NewPNCounter(crdt.NodeID(id))
		replicators[i] = crdt.New(id, b.send(id))
		replicators[i].Register("tokens", counters[i], "pncounter")
		b.add(id, replicators[i])
	}

	// Distribute operations across the three nodes concurrently.
	var wg sync.WaitGroup
	wg.Add(3)

	// Distribute increments: ops/3 per node.
	incsPerNode := *ops / 3
	remainder := *ops % 3

	// Distribute decrements: decs/3 per node.
	decsPerNode := *decs / 3
	decRemainder := *decs % 3

	for i, id := range nodeIDs {
		go func(idx int, nodeID string) {
			defer wg.Done()

			// Distribute any remainder of incs to the first nodes.
			incs := incsPerNode
			if idx < remainder {
				incs++
			}

			// Distribute any remainder of decs to the first nodes.
			decs := decsPerNode
			if idx < decRemainder {
				decs++
			}

			// Apply increments and decrements, replicating after each.
			for j := 0; j < incs; j++ {
				counters[idx].Inc(1)
				if err := replicators[idx].Replicate("tokens"); err != nil {
					fmt.Fprintf(os.Stderr, "node %s Inc replicate: %v\n", nodeID, err)
					os.Exit(1)
				}
			}

			for j := 0; j < decs; j++ {
				counters[idx].Dec(1)
				if err := replicators[idx].Replicate("tokens"); err != nil {
					fmt.Fprintf(os.Stderr, "node %s Dec replicate: %v\n", nodeID, err)
					os.Exit(1)
				}
			}
		}(i, id)
	}

	wg.Wait()

	// Perform final replication round to ensure all nodes converge.
	for i, id := range nodeIDs {
		if err := replicators[i].Replicate("tokens"); err != nil {
			fmt.Fprintf(os.Stderr, "node %s final replicate: %v\n", id, err)
			os.Exit(1)
		}
	}

	// Verify convergence: all nodes should have the same value.
	expected := int64(*ops - *decs)
	fmt.Printf("Expected value: %d (ops=%d, decs=%d)\n", expected, *ops, *decs)

	allMatch := true
	for i, id := range nodeIDs {
		val := counters[i].Value()
		fmt.Printf("Node %s: %d\n", id, val)
		if val != expected {
			allMatch = false
		}
	}

	if !allMatch {
		fmt.Fprintf(os.Stderr, "ERROR: not all nodes converged to %d\n", expected)
		os.Exit(1)
	}

	fmt.Println("SUCCESS: all replicas converged")
}
