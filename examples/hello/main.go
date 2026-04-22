// hello spawns a single greeter actor and sends it two messages.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
)

type greeter struct{}

func (greeter) Receive(ctx actor.Context, msg any) error {
	fmt.Printf("[%s] hello, %v\n", ctx.Self().PID(), msg)
	return nil
}

func main() {
	sys := actor.NewSystem("hello")

	ref, err := sys.Spawn(actor.Spec{
		Name:    "greeter",
		Factory: func() actor.Actor { return greeter{} },
	})
	if err != nil {
		panic(err)
	}

	_ = ref.Tell("world")
	_ = ref.Tell("tinyagents")

	// Give the actor a moment to drain — Tell is fire-and-forget.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sys.Stop(ctx); err != nil {
		panic(err)
	}
}
