// tinyagentsctl is the operator utility for tinyagents clusters.
//
// Usage:
//
//	tinyagentsctl <command> [flags]
//
// Commands:
//
//	version   Print version info
//	status    Print local runtime status
//	members   Join a cluster, list members, leave
//
// Run 'tinyagentsctl <command> -h' for command-specific help.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version":
		runVersion()
	case "status":
		runStatus()
	case "members":
		runMembers(args)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "tinyagentsctl: unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`tinyagentsctl — operator utility for tinyagents clusters

Usage: tinyagentsctl <command> [flags]

Commands:
  version           Print version info
  status            Print local runtime status
  members           Join a cluster, list members, leave

Run 'tinyagentsctl <command> -h' for command-specific help.
`)
}
