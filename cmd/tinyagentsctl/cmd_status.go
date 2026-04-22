package main

import (
	"fmt"
	"os"
	"runtime"
)

func runStatus() {
	fmt.Println("tinyagentsctl status")
	fmt.Printf("  version:    %s\n", Version)
	fmt.Printf("  go:         %s\n", runtime.Version())
	fmt.Printf("  os/arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  pid:        %d\n", os.Getpid())
	fmt.Printf("  cpus:       %d\n", runtime.NumCPU())
}
