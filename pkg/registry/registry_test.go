package registry_test

import (
	"context"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/registry"
)

func TestSystemSatisfiesRegistry(t *testing.T) {
	t.Parallel()
	sys := actor.NewSystem("reg")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	})

	// Compile-time + runtime: *actor.System implements registry.Registry.
	var r registry.Registry = sys

	a, _ := sys.Spawn(actor.Spec{
		Name: "alpha",
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(actor.Context, any) error { return nil })
		},
	})
	b, _ := sys.Spawn(actor.Spec{
		Name: "beta",
		Factory: func() actor.Actor {
			return actor.ActorFunc(func(actor.Context, any) error { return nil })
		},
	})

	got, ok := r.Lookup("/user/alpha")
	if !ok {
		t.Fatalf("Lookup(/user/alpha) missing")
	}
	if got.PID() != a.PID() {
		t.Fatalf("Lookup returned %v, want %v", got.PID(), a.PID())
	}
	if _, ok := r.Lookup("/user/nope"); ok {
		t.Fatal("Lookup returned a Ref for an unknown path")
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	paths := map[string]bool{}
	for _, ref := range list {
		paths[ref.PID().Path] = true
	}
	if !paths["/user/alpha"] || !paths["/user/beta"] {
		t.Fatalf("List missing entries: %v", paths)
	}

	_ = b.Stop()
	// Wait for the runloop to unregister.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(r.List()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(r.List()) != 1 {
		t.Fatalf("after Stop List len = %d, want 1", len(r.List()))
	}
}
