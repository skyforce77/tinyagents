package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/skyforce77/tinyagents/pkg/cluster"
	"github.com/skyforce77/tinyagents/pkg/cluster/memberlist"
)

func runMembers(args []string) {
	fs := flag.NewFlagSet("members", flag.ContinueOnError)
	seeds := fs.String("seeds", "", "Comma-separated seed addresses (host:port). Required.")
	bind := fs.String("bind", "127.0.0.1:0", "Local gossip bind address (host:port).")
	id := fs.String("id", "observer", "Node ID for this ephemeral member.")
	timeout := fs.Duration("timeout", 10*time.Second, "How long to wait for membership to settle.")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "tinyagentsctl: %v\n", err)
		os.Exit(1)
	}

	if *seeds == "" {
		fmt.Fprintln(os.Stderr, "tinyagentsctl: -seeds is required (comma-separated host:port list)")
		os.Exit(1)
	}

	seedList := strings.Split(*seeds, ",")
	for i, s := range seedList {
		seedList[i] = strings.TrimSpace(s)
	}

	c, err := memberlist.New(*id, *bind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tinyagentsctl: failed to create cluster member: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Subscribe before joining so we catch the initial join events.
	settled := make(chan struct{}, 1)
	unsub := c.Subscribe(func(_ cluster.Event) {
		select {
		case settled <- struct{}{}:
		default:
		}
	})

	if err := c.Join(ctx, seedList...); err != nil {
		fmt.Fprintf(os.Stderr, "tinyagentsctl: join failed: %v\n", err)
		os.Exit(1)
	}

	// Wait for at least one membership event or timeout.
	select {
	case <-settled:
	case <-ctx.Done():
		// Timeout is not fatal — print whatever we have.
	}
	unsub()

	// Poll for a stable view (200 ms grace period after first event).
	timer := time.NewTimer(200 * time.Millisecond)
	select {
	case <-timer.C:
	case <-ctx.Done():
	}

	members := c.Members()

	leaveCtx, leaveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer leaveCancel()
	if err := c.Leave(leaveCtx); err != nil {
		fmt.Fprintf(os.Stderr, "tinyagentsctl: leave error: %v\n", err)
	}

	printMembersTable(members)
}

// printMembersTable renders members as an aligned three-column table.
func printMembersTable(members []cluster.Member) {
	const colID = 20
	const colAddr = 23

	header := fmt.Sprintf("%-*s%-*s%s", colID, "ID", colAddr, "ADDRESS", "META")
	fmt.Println(header)

	for _, m := range members {
		meta := renderMeta(m.Meta)
		fmt.Printf("%-*s%-*s%s\n", colID, m.ID, colAddr, m.Address, meta)
	}
}

// renderMeta returns "-" for an empty map, or "k1=v1,k2=v2" sorted by key.
func renderMeta(meta map[string]string) string {
	if len(meta) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+meta[k])
	}
	return strings.Join(parts, ",")
}
