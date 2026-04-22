// ask demonstrates the request/reply pattern with Ref.Ask and a deadline.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
)

type upper struct{}

func (upper) Receive(ctx actor.Context, msg any) error {
	s, ok := msg.(string)
	if !ok {
		return ctx.Respond(fmt.Errorf("want string, got %T", msg))
	}
	return ctx.Respond(strings.ToUpper(s))
}

func main() {
	sys := actor.NewSystem("ask")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	}()

	ref, err := sys.Spawn(actor.Spec{
		Name:    "upper",
		Factory: func() actor.Actor { return upper{} },
	})
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	resp, err := ref.Ask(ctx, "hello tinyagents")
	if err != nil {
		panic(err)
	}
	fmt.Println("reply:", resp)
}
