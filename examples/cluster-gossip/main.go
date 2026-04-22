// cluster-gossip demonstrates a 3-node memberlist Cluster gossip demo
// where alpha bootstraps alone, beta and gamma join alpha as seed,
// and each node prints its membership view before leaving in reverse order.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/skyforce77/tinyagents/pkg/cluster"
	"github.com/skyforce77/tinyagents/pkg/cluster/memberlist"
)

func main() {
	delay := flag.Duration("delay", 1500*time.Millisecond, "sleep after each Join/Leave to let gossip converge")
	flag.Parse()

	// Create three cluster instances on ephemeral 127.0.0.1 ports.
	alpha, err := memberlist.New("alpha", "127.0.0.1:0")
	if err != nil {
		exitf("create alpha: %v", err)
	}
	defer alpha.Close()

	beta, err := memberlist.New("beta", "127.0.0.1:0")
	if err != nil {
		exitf("create beta: %v", err)
	}
	defer beta.Close()

	gamma, err := memberlist.New("gamma", "127.0.0.1:0")
	if err != nil {
		exitf("create gamma: %v", err)
	}
	defer gamma.Close()

	// Bootstrap: alpha joins alone (no seeds).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := alpha.Join(ctx, nil...); err != nil {
		cancel()
		exitf("alpha join: %v", err)
	}
	cancel()

	time.Sleep(*delay)

	// Get alpha's address after it's joined and bound.
	alphaAddr := alpha.LocalNode().Address
	fmt.Printf("Alpha bootstrapped at %s\n", alphaAddr)

	// Beta joins with alpha as seed.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	if err := beta.Join(ctx, alphaAddr); err != nil {
		cancel()
		exitf("beta join: %v", err)
	}
	cancel()

	time.Sleep(*delay)

	// Gamma joins with alpha as seed.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	if err := gamma.Join(ctx, alphaAddr); err != nil {
		cancel()
		exitf("gamma join: %v", err)
	}
	cancel()

	time.Sleep(*delay)

	// All three are now joined. Print each node's membership view.
	fmt.Println("\n=== All three nodes joined ===")
	printMembers("alpha", alpha.Members())
	printMembers("beta", beta.Members())
	printMembers("gamma", gamma.Members())

	// Leave in reverse order: gamma, beta, alpha.
	fmt.Println("\n=== Leaving in reverse order ===")

	// Leave gamma.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	if err := gamma.Leave(ctx); err != nil {
		cancel()
		exitf("gamma leave: %v", err)
	}
	cancel()

	time.Sleep(*delay)

	fmt.Println("\nAfter gamma left:")
	printMembers("alpha", alpha.Members())
	printMembers("beta", beta.Members())

	// Leave beta.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	if err := beta.Leave(ctx); err != nil {
		cancel()
		exitf("beta leave: %v", err)
	}
	cancel()

	time.Sleep(*delay)

	fmt.Println("\nAfter beta left:")
	printMembers("alpha", alpha.Members())

	// Leave alpha.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	if err := alpha.Leave(ctx); err != nil {
		cancel()
		exitf("alpha leave: %v", err)
	}
	cancel()

	fmt.Println("\nCluster demo complete.")
}

func printMembers(nodeID string, members []cluster.Member) {
	fmt.Printf("%s's view: [", nodeID)
	for i, m := range members {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s@%s", m.ID, m.Address)
	}
	fmt.Println("]")
}

func exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
