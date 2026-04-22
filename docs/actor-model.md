# The Actor Model in tinyagents

This document is a deep dive into `pkg/actor`. It explains why tinyagents
uses an actor model, walks through every public type with runnable code, and
covers mailboxes, supervision, routers, the registry, and the watch/terminate
lifecycle.

See also:
[architecture.md](architecture.md) |
[agents-and-teams.md](agents-and-teams.md) |
[clustering.md](clustering.md) |
[llm-providers.md](llm-providers.md)

---

## Why an Actor Model?

LLM agents are I/O-heavy: they block on network calls, accumulate conversation
history, invoke tools, and retry on transient failures. Concurrency is
therefore not an afterthought — it is the default.

The actor model brings three properties that compose well with this workload:

**Isolation.** Each actor owns its own state and never shares it with other
actors. An agent's conversation transcript, its in-progress tool invocations,
and its running token total are private to one goroutine. There are no mutexes
on the hot path, no data races to hunt down, and no "who modified this field?"
debugging sessions.

**Message-ordering guarantees per actor.** Every actor processes exactly one
message at a time, in the order messages arrived in its mailbox. An agent
processing a `Prompt` will finish — or deliberately fail — before it touches
the next message. This makes reasoning about agent state sequential even when
the surrounding system is highly concurrent.

**Supervision.** Failures are first-class. When an actor's `Receive` panics
or returns an error, the supervisor attached to that actor decides what to do:
restart it (possibly with exponential backoff), resume as if nothing happened,
stop it cleanly, or escalate to the parent. The rest of the system keeps
running. For LLM agents this matters because provider outages, tool panics,
and budget exhaustion are all expected events, not exceptional ones.

---

## Core Types

### Actor

`Actor` is the unit of behavior. The only method is `Receive`:

```go
type Actor interface {
    Receive(ctx Context, msg any) error
}
```

Returning a non-nil error from `Receive`, or allowing a panic to propagate,
hands control to the supervisor attached to this actor. A nil return means
"processed successfully."

For small actors that do not need their own struct, `ActorFunc` adapts a
closure:

```go
greeter := actor.ActorFunc(func(ctx actor.Context, msg any) error {
    ctx.Log().Info("received", "msg", msg)
    return ctx.Respond("hello")
})
```

### Spec

`Spec` describes how to spawn an actor. Only `Factory` is required:

```go
spec := actor.Spec{
    Name:    "greeter",          // last path segment; auto-generated if empty
    Factory: func() actor.Actor { return &myActor{} },
    Mailbox: mailbox.Config{
        Capacity: 100,
        Policy:   mailbox.Block,
    },
    Supervisor: supervisor.Default(), // nil also picks Default
}
```

`Factory` is called once at spawn and once on every supervisor-driven restart.
Do not capture externally-shared state that assumes single-instance ownership
inside the factory closure — anything that must survive a restart should live
outside and be passed in as a dependency.

### System

`System` is the root of all actors in a process. Create one with `NewSystem`:

```go
sys := actor.NewSystem("my-node",
    actor.WithLogger(slog.Default()),
)
defer sys.Stop(context.Background())
```

The system name becomes the `Node` segment of every `PID` it owns. Top-level
actors land under `/user`:

```go
ref, err := sys.Spawn(spec)
// ref.PID() == {Node: "my-node", Path: "/user/greeter"}
```

Stopping the system cancels every actor's context in order, closing mailboxes
and waiting for all run loops to exit.

### Ref

`Ref` is the only way to address an actor. It is safe to share across
goroutines and across packages.

```go
// Fire-and-forget: returns once the envelope is enqueued (or the mailbox errs).
err := ref.Tell(someMessage)

// Request-reply: blocks until the actor calls ctx.Respond, or ctx times out.
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
reply, err := ref.Ask(ctx, someMessage)

// Graceful stop: closes the mailbox and cancels the actor's context.
err = ref.Stop()
```

`Ask` creates a `chan any` as the reply destination and embeds it in the
envelope. The actor calls `ctx.Respond(msg)` to send the reply back down that
channel.

### Context

`Context` is handed to every `Receive` call. It embeds `context.Context` (the
actor's own context, cancelled when the actor is stopped) and adds
actor-system operations:

```go
func (a *MyActor) Receive(ctx actor.Context, msg any) error {
    // Spawn a child actor; it inherits this actor's lifecycle context.
    child, err := ctx.Spawn(childSpec)

    // Send to another actor, identifying ourselves as sender.
    ctx.Tell(otherRef, SomeMessage{})

    // Reply to whoever sent this message (Ask or Tell).
    ctx.Respond(Result{Value: 42})

    // Per-actor store (single-goroutine; no synchronization needed).
    ctx.Store().Set("key", value)

    // System-wide store (concurrent-safe; shared by all actors).
    ctx.Global().Set("shared-key", value)

    // Structured logger pre-tagged with the actor's PID.
    ctx.Log().Info("handling message", "type", fmt.Sprintf("%T", msg))

    return nil
}
```

`ctx.Forward(target, msg)` re-sends the current message while preserving the
original `Sender`, which ensures that `Ask`-driven replies still reach the
original caller when a router sits in the middle.

---

## Mailbox Configuration

Every actor has a mailbox fronted by `mailbox.Mailbox`. The default
(zero `mailbox.Config`) is an unbounded queue backed by a slice that grows as
needed. For actors where unbounded growth would hide a capacity problem, use a
bounded mailbox:

```go
spec := actor.Spec{
    Factory: myFactory,
    Mailbox: mailbox.Config{
        Capacity: 256,
        Policy:   mailbox.DropOldest,
    },
}
```

The four backpressure policies:

| Policy | Behavior when the mailbox is full |
|---|---|
| `Block` | The `Tell` / `Ask` call blocks until space is available or ctx is cancelled. |
| `DropNewest` | The incoming envelope is silently dropped; `Tell` returns nil. |
| `DropOldest` | The oldest envelope is evicted to make room. |
| `Fail` | `Tell` returns `mailbox.ErrMailboxFull`. |

`Cap()` returns the configured capacity; `Len()` returns the current backlog.
Both are used by the `LeastLoaded` router to pick the least-backlogged worker.

---

## Supervision Strategies

A supervisor `Strategy` is consulted every time an actor fails (panic or
returned error). It returns a `Decision{Directive, Delay}`.

The four directives:

| Directive | Meaning |
|---|---|
| `Resume` | Ignore the failure and process the next message. The actor instance is not replaced. |
| `Restart` | Recreate the actor via its `Spec.Factory`. Optionally delay by `Decision.Delay` to implement backoff. |
| `Stop` | Terminate the actor without restarting. Watchers receive `Terminated`. |
| `Escalate` | Forward a `Failed` message to the parent actor and then stop. The parent's `Receive` handles it. |

**Default strategy.** When `Spec.Supervisor` is nil, `supervisor.Default()` is
used: restart up to 5 times within a 1-minute window, with exponential backoff
from 50 ms to 1 s. After the 5th restart within the window, the actor is
stopped.

**Custom restart.** Use `supervisor.NewRestart` to tune the parameters:

```go
import "github.com/skyforce77/tinyagents/pkg/supervisor"

spec := actor.Spec{
    Factory: myFactory,
    Supervisor: supervisor.NewRestart(
        10,             // max restarts
        2*time.Minute,  // window
        100*time.Millisecond, // base backoff
        30*time.Second,       // max backoff
    ),
}
```

**Stop-only strategy.** For actors that must not restart on failure:

```go
Supervisor: supervisor.NewStop()
```

**Resume strategy.** Implement `Strategy` directly for custom logic:

```go
type alwaysResume struct{}
func (alwaysResume) Decide(any, int, time.Time) supervisor.Decision {
    return supervisor.Decision{Directive: supervisor.Resume}
}
```

---

## Routers

A router is itself an actor that fronts a pool of worker actors. Send messages
to the router's `Ref`; the router forwards each one to a worker. Workers are
spawned lazily on the first message and are children of the router, so stopping
the router cascades to every worker.

```go
import "github.com/skyforce77/tinyagents/pkg/router"

workerSpec := actor.Spec{
    Factory: func() actor.Actor { return &myWorker{} },
}

ref, err := router.Spawn(sys, workerSpec, router.RoundRobin, 4, router.Config{})
```

The five routing kinds:

| Kind | Dispatch algorithm |
|---|---|
| `RoundRobin` | Rotating sequence over the pool, O(1). |
| `Random` | Uniform random pick, O(1). |
| `Broadcast` | Every worker receives every message. Use for fan-out work. |
| `ConsistentHash` | FNV-32a hash of a key extracted from the message; same key always routes to the same worker. Messages implement `HashKeyer` or `Config.HashKey` provides the extractor. |
| `LeastLoaded` | Picks the worker with the smallest `mailbox.Len()` backlog; falls back to round-robin when no worker implements `LoadReporter`. |

**RoundRobin example:**

```go
ref, err := router.Spawn(sys, actor.Spec{
    Name: "summarizer-pool",
    Factory: func() actor.Actor {
        return &SummarizerActor{}
    },
    Mailbox: mailbox.Config{Capacity: 64, Policy: mailbox.Block},
}, router.RoundRobin, 8, router.Config{})
if err != nil {
    log.Fatal(err)
}

// The pool of 8 workers will receive these in rotating order.
for _, doc := range docs {
    ref.Tell(SummarizeRequest{Text: doc})
}
```

**ConsistentHash example.** Messages that implement `HashKeyer` carry their
own routing key:

```go
type UserPrompt struct {
    UserID string
    Text   string
}

func (u UserPrompt) HashKey() string { return u.UserID }

ref, err := router.Spawn(sys, workerSpec, router.ConsistentHash, 4, router.Config{})
// Prompts from the same UserID always reach the same worker,
// so per-user in-memory state stays coherent.
```

---

## Local vs. Cluster-Aware Registry

In a single-node deployment `*actor.System` itself satisfies `registry.Registry`
through its `Lookup(path)` and `List()` methods. For multi-node deployments,
`registry.NewClusterRegistry` wraps any `Registry` with a consistent-hash ring
over the cluster membership, adding `OwnerOf(path)` and `IsLocal(path)`.

See [clustering.md](clustering.md) for the full treatment.

---

## Watch / Terminated / Failed

Actors can subscribe to each other's termination events. The watch API is
available on `actor.Context`:

```go
func (a *Supervisor) Receive(ctx actor.Context, msg any) error {
    switch m := msg.(type) {
    case actor.Terminated:
        ctx.Log().Info("watched actor stopped", "pid", m.PID, "reason", m.Reason)
        // Optional: respawn, alert, adjust routing table, etc.

    case actor.Failed:
        // Delivered when a child used Escalate; m.Child is the failed PID.
        ctx.Log().Warn("child escalated failure", "child", m.Child, "cause", m.Cause)

    default:
        child, _ := ctx.Spawn(childSpec)
        ctx.Watch(child)
    }
    return nil
}
```

`Watch` is idempotent (calling it twice on the same target is a no-op).
Watching a `Ref` that has already stopped delivers `Terminated` immediately.

`Unwatch` removes the subscription if it is no longer needed:

```go
ctx.Unwatch(child)
```

`Terminated.Reason` is non-nil only when the actor stopped due to an
unrecoverable failure (the supervisor chose `Stop` after exhausting restarts,
or `Escalate` was used).

---

## Gotchas

**Capturing loop variables in factories.** A factory closure that captures a
loop variable by reference will see the final value of that variable on every
restart. Capture by value instead:

```go
for i, cfg := range configs {
    i, cfg := i, cfg  // shadow with value copies
    specs[i] = actor.Spec{
        Factory: func() actor.Actor { return newWorker(cfg) },
    }
}
```

**Panics inside Receive.** A panic is treated the same as a returned error:
the supervisor decides what to do. Do not recover panics inside `Receive`
unless you need custom logic; let the supervisor handle it. Recovering and
returning nil signals "success" to the supervisor and resets the failure count.

**Synchronous Ask inside Receive.** Calling `ref.Ask(ctx, msg)` inside
`Receive` is safe in most cases, but watch for cycles: if actor A asks actor B,
and B asks A back before replying to A, A's mailbox cannot drain (it is blocked
in Ask) and B's Ask will time out. Design message flows as DAGs, not cycles.

**Blocking I/O inside Receive.** Long-running I/O inside `Receive` delays
every subsequent message in the mailbox. Use a bounded mailbox with `Block`
policy and size the pool (via a router) so that backlog pressure is visible
rather than hidden behind an unbounded queue.

**Dead Ref after Stop.** Calling `Tell` or `Ask` on a stopped `Ref` returns
`"actor: dead ref"`. If you retain Refs in a registry, clean them up on
`Terminated`.

**Spec.Factory and shared Memory.** `agent.Spec` copies the `Agent` template
struct on each restart — including the `Memory` pointer. If you want memory to
reset on restart, build a fresh `memory.Memory` inside the factory:

```go
agent.Spec("chat", agent.Agent{
    // ...
    // Memory: nil  ← leave nil so the Agent creates an ephemeral buffer per prompt
})
```
