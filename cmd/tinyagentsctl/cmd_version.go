package main

import (
	"fmt"
	"runtime"
)

// Version is the binary version. Override at link time with:
//
//	-ldflags "-X main.Version=v1.2.3"
var Version = "dev"

func runVersion() {
	fmt.Printf("tinyagentsctl %s\n", Version)
	fmt.Printf("go %s\n", runtime.Version())
}
