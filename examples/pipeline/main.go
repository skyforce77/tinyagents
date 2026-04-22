// pipeline chains three actors — tokenizer → uppercaser pool → collector —
// to show how Forward, RoundRobin routing, and a join actor compose end to
// end.
//
//	tokenizer ──(Word)──▶ upperPool (RR, 3 workers) ──(Word)──▶ collector
//	         └─(Total)──────────────────────────────────────────▶
//
// The collector waits until it has heard both the expected total and every
// word, reorders them by sequence number, and signals main via a channel.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/router"
)

// Messages flowing through the pipeline.

type sentence struct{ Text string }

type total struct{ N int }

type word struct {
	Seq  int
	Text string
}

// tokenizer splits the incoming sentence into words, tells the collector how
// many to expect, and fires each word at the uppercase pool.
type tokenizer struct {
	upper     actor.Ref
	collector actor.Ref
}

func (t *tokenizer) Receive(ctx actor.Context, msg any) error {
	s, ok := msg.(sentence)
	if !ok {
		return nil
	}
	words := strings.Fields(s.Text)
	if err := ctx.Tell(t.collector, total{N: len(words)}); err != nil {
		return err
	}
	for i, w := range words {
		if err := ctx.Tell(t.upper, word{Seq: i, Text: w}); err != nil {
			return err
		}
	}
	return nil
}

// uppercaser is one worker of the RoundRobin pool. It uppercases a single
// word and forwards it to the collector.
type uppercaser struct {
	next actor.Ref
}

func (u *uppercaser) Receive(ctx actor.Context, msg any) error {
	w, ok := msg.(word)
	if !ok {
		return nil
	}
	return ctx.Tell(u.next, word{Seq: w.Seq, Text: strings.ToUpper(w.Text)})
}

// collector joins words and the expected total, then signals main when the
// full sentence has been received.
type collector struct {
	done     chan<- string
	received map[int]string
	expected int
	hasTotal bool
}

func (c *collector) Receive(ctx actor.Context, msg any) error {
	switch m := msg.(type) {
	case total:
		c.expected = m.N
		c.hasTotal = true
	case word:
		c.received[m.Seq] = m.Text
	}
	if c.hasTotal && len(c.received) >= c.expected {
		keys := make([]int, 0, c.expected)
		for k := range c.received {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		out := make([]string, 0, c.expected)
		for _, k := range keys {
			out = append(out, c.received[k])
		}
		select {
		case c.done <- strings.Join(out, " "):
		default:
		}
	}
	return nil
}

func main() {
	sys := actor.NewSystem("pipeline")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	}()

	done := make(chan string, 1)

	collectorRef, err := sys.Spawn(actor.Spec{
		Name: "collector",
		Factory: func() actor.Actor {
			return &collector{done: done, received: map[int]string{}}
		},
	})
	must(err)

	upperPool, err := router.Spawn(sys, actor.Spec{
		Name: "upper",
		Factory: func() actor.Actor {
			return &uppercaser{next: collectorRef}
		},
	}, router.RoundRobin, 3, router.Config{})
	must(err)

	tokenizerRef, err := sys.Spawn(actor.Spec{
		Name: "tokenizer",
		Factory: func() actor.Actor {
			return &tokenizer{upper: upperPool, collector: collectorRef}
		},
	})
	must(err)

	must(tokenizerRef.Tell(sentence{Text: "hello tinyagents from the pipeline example"}))

	select {
	case out := <-done:
		fmt.Println("pipeline output:", out)
	case <-time.After(time.Second):
		fmt.Println("pipeline timed out")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
