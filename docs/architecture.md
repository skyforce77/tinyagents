# tinyagents — System Architecture

This document is the system-level tour of tinyagents. It covers the layered
package structure, end-to-end message flow, the design principles that shaped
every API, and a package index you can use as a map when exploring the source.

See also:
[actor-model.md](actor-model.md) |
[agents-and-teams.md](agents-and-teams.md) |
[clustering.md](clustering.md) |
[llm-providers.md](llm-providers.md)

---

## Overview

tinyagents is a Go library for building LLM-agent systems on an actor model.
It is not a framework with a daemon, a sidecar, or a required deployment
topology. You import it, create a `*actor.System`, spawn actors (including
`*agent.Agent` instances), and wire the pieces together in ordinary Go code.
tinyagents is opinionated about actor boundaries — every agent is an isolated
goroutine, every inter-agent communication is a mailbox message — and
deliberately unopinionated about everything else: you choose the LLM providers,
the transport topology, the memory implementation, and how many nodes you run.

---

## Layer Diagram

```
┌──────────────────────────────────────────────────────────────┐
│  Application code / examples                                 │
├────────────────────────────┬─────────────────────────────────┤
│  pkg/team                  │  pkg/agent                      │
│  (Pipeline, Broadcast,     │  (Agent actor, Policy, Budget,  │
│   Router, Hierarchy,       │   Prompt/Response/Error msgs)   │
│   Debate)                  │                                 │
├────────────────────────────┴─────────────────────────────────┤
│  pkg/actor           │ pkg/llm            │ pkg/tool         │
│  (Actor, Ref,        │ (Provider, Chat,   │ (Tool, Registry) │
│   Context, Spec,     │  Stream, Embed,    │                  │
│   System, PID)       │  Middleware stack) │ pkg/memory       │
│                      │                   │ (Memory, buffer/) │
│  pkg/mailbox         │ pkg/llm/openai    │                   │
│  (Mailbox, Config,   │ pkg/llm/anthropic │                   │
│   backpressure)      │ pkg/llm/ollama    │                   │
│                      │ pkg/llm/mistral   │                   │
│  pkg/supervisor      │ pkg/llm/openrouter│                   │
│  (Strategy, Restart) │                   │                   │
│                      │                   │                   │
│  pkg/router          │                   │                   │
│  (RoundRobin,Random, │                   │                   │
│   Broadcast,         │                   │                   │
│   ConsistentHash,    │                   │                   │
│   LeastLoaded)       │                   │                   │
│                      │                   │                   │
│  pkg/registry        │                   │                   │
│  (Registry,          │                   │                   │
│   ClusterRegistry)   │                   │                   │
├──────────────────────┴───────────────────┴───────────────────┤
│  Distributed mode (experimental)                             │
│  pkg/cluster          pkg/transport        pkg/crdt          │
│  (Cluster, Member,    (Transport, Conn,    (GCounter,        │
│   local, memberlist)   Handler)             PNCounter,       │
│                                             LWWRegister,     │
│  internal/proto                             ORSet,           │
│  (Envelope, PID,                            Replicator)      │
│   Heartbeat, Kind)                                           │
│                                                              │
│  internal/wire                                               │
│  (Codec/GobCodec, framing, Heartbeater)                      │
└──────────────────────────────────────────────────────────────┘
```

---

## Message Flow

The path from an external caller through an `Agent` to the provider and back,
annotated with the goroutines involved:

```
Caller goroutine                         Agent goroutine
─────────────────                        ───────────────
ref.Ask(ctx, agent.Prompt{Text: "..."})
 │
 │  builds mailbox.Envelope{Msg: Prompt, ReplyTo: chan any}
 │  Enqueue → actor mailbox
 │                                       Dequeue from mailbox
 │                                       actor.Context constructed
 │                                       Agent.Receive(ctx, Prompt)
 │                                         mem.Append(userMsg)
 │                                         for turn := range maxTurns:
 │                                           if Budget: Budget.Reserve()
 │                                           build llm.ChatRequest
 │                                           Provider.Chat(ctx, req)  ←── HTTP goroutine(s)
 │                                             ↓ llm.ChatResponse
 │                                           if ToolCalls:
 │                                             tool.Invoke(ctx, args) ←── tool goroutine(s)
 │                                             mem.Append(toolMsg)
 │                                             continue
 │                                           mem.Append(assistantMsg)
 │                                           done = true
 │                                         Budget.Record(usage)
 │                                         ctx.Respond(agent.Response{…})
 │                                           replyTo chan <- Response
 │  select: <-replyTo
 │  return Response, nil
```

For streaming prompts (`Prompt.Stream != nil`), `Provider.Stream` is called
instead of `Provider.Chat`, and each `llm.Chunk` is forwarded to
`Prompt.Stream` while the Agent assembles the aggregated message. The channel
is closed by the Agent before it sends the final `agent.Response`.

---

## Package Index

| Package | One-line purpose | Status |
|---|---|---|
| `pkg/actor` | Actor, Ref, Context, Spec, System, PID — the runtime core | stable v0.1 |
| `pkg/mailbox` | Envelope queue with bounded/unbounded and backpressure policies | stable v0.1 |
| `pkg/supervisor` | Restart strategies (Restart/Resume/Stop/Escalate) with backoff | stable v0.1 |
| `pkg/router` | Pool-of-workers dispatchers: RoundRobin, Random, Broadcast, ConsistentHash, LeastLoaded | stable v0.1 |
| `pkg/registry` | Read-only actor lookup (local); ClusterRegistry for consistent-hash ring | stable v0.1 (local); experimental (cluster) |
| `pkg/llm` | Provider interface, ChatRequest/Response/Chunk, Message, Middleware, Registry | stable v0.1 |
| `pkg/llm/openai` | Adapter for OpenAI Chat Completions and wire-compatible backends | stable v0.1 |
| `pkg/llm/anthropic` | Adapter for Anthropic Messages API | stable v0.1 |
| `pkg/llm/ollama` | Adapter for local Ollama server | stable v0.1 |
| `pkg/llm/mistral` | Thin openai-mode wrapper for Mistral | stable v0.1 |
| `pkg/llm/openrouter` | Thin openai-mode wrapper for OpenRouter | stable v0.1 |
| `pkg/tool` | Tool interface, Func adapter, Registry | stable v0.1 |
| `pkg/memory` | Memory interface (Append/Window/Search) | stable v0.1 |
| `pkg/memory/buffer` | In-process ring buffer (no Search support) | stable v0.1 |
| `pkg/memory` (beyond buffer) | Vector store, sqlite, semantic search backends | experimental |
| `pkg/agent` | Agent actor: drives an LLM Provider through tool-use loops | stable v0.1 |
| `pkg/team` | Pipeline, Broadcast, Router, Hierarchy, Debate coordinators | stable v0.1 |
| `pkg/cluster` | Cluster interface, local impl, memberlist/gossip impl | experimental |
| `pkg/transport` | Transport/Conn interface for cross-node delivery | experimental |
| `pkg/transport/tcp` | TCP transport with framing, heartbeat, and GobCodec | experimental |
| `pkg/crdt` | GCounter, PNCounter, LWWRegister, ORSet, Replicator | experimental |
| `internal/proto` | Wire envelope and PID types (zero deps on pkg/) | internal |
| `internal/wire` | GobCodec, length-prefixed framing, Heartbeater | internal |

---

## Design Principles

- **Interface-first.** Every layer (`Actor`, `Mailbox`, `Provider`, `Memory`,
  `Tool`, `Cluster`, `Transport`, `CRDT`) is defined as a Go interface before
  any concrete type. This keeps test doubles cheap and backends swappable.

- **Per-agent provider.** `pkg/llm.Provider` is passed into each `agent.Agent`
  as a plain value. There is no global provider registry you must populate
  before using the library. A single `*actor.System` can simultaneously back
  agents on Ollama, Anthropic, OpenAI, and Mistral — `examples/team-mixed-providers`
  demonstrates exactly this.

- **No global state.** `actor.NewSystem` is the root of all actor state.
  `llm.NewRegistry` is optional and scoped to a variable. `tool.NewRegistry`
  is per-instance. If you need two independent systems in one process (e.g.
  in tests), nothing stops you.

- **One goroutine per actor.** Each actor's `Receive` runs serially in its own
  goroutine, reading one message at a time from its mailbox. Actor state
  therefore needs no synchronization. Shared state (budgets, global stores,
  CRDTs) is explicitly concurrent-safe.

- **Mailbox-driven backpressure.** Bounded mailboxes expose four policies
  (`Block`, `DropNewest`, `DropOldest`, `Fail`). Senders that exceed mailbox
  capacity cannot silently overwhelm receivers; they feel the backpressure or
  get an error they can route through the supervisor.

- **CRDT for cluster-wide state.** tinyagents adopts conflict-free replicated
  data types for the small set of cluster-wide counters and registers that
  agents or teams need (token budgets, request counts, feature flags). CRDTs
  converge without coordination, which matches the eventual-consistency
  guarantee of the gossip membership layer.

- **Opinionated on message shapes, not on deployment.** `agent.Prompt` and
  `agent.Response` are stable, provider-neutral message types. `team`
  coordinators speak only those types, so teams nest cleanly. The library
  does not care whether you run one node or ten.

---

## When to Reach for tinyagents

tinyagents is a good fit when you want:

- A single binary that orchestrates multiple LLM agents, each on its own
  goroutine, without the overhead of an external message broker.
- Heterogeneous provider mixes inside one system (local models alongside
  hosted APIs, for example).
- Agent coordination patterns (pipeline, broadcast fan-out, hierarchical
  synthesis, structured debate) that compose rather than couple.
- Gradual adoption: the actor layer can be used without any LLM code, and the
  LLM layer can be used as a standalone provider-neutral client without spinning
  up an actor system.

If your workload requires millions of actors, fine-grained per-message
persistence, or cross-datacenter strong consistency, tinyagents' current scope
will not be enough and you should reach for a library designed specifically for
those properties.

---

## Reading List

- [actor-model.md](actor-model.md) — deep dive on `pkg/actor`: mailboxes,
  supervision strategies, routers, watches.
- [agents-and-teams.md](agents-and-teams.md) — `pkg/agent` and `pkg/team`:
  message shapes, tool-use loops, streaming, budget, all five team patterns.
- [clustering.md](clustering.md) — `pkg/cluster`, `pkg/transport`, `pkg/crdt`:
  gossip membership, consistent-hash registry, CRDT state.
- [llm-providers.md](llm-providers.md) — `pkg/llm`: provider interface,
  built-in adapters, middleware stack, streaming, adding a new adapter.
