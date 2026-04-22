# tinyagents

A minimalist actor system for LLM agent orchestration in Go — each agent owns its provider.

[![Go Reference](https://pkg.go.dev/badge/github.com/skyforce77/tinyagents.svg)](https://pkg.go.dev/github.com/skyforce77/tinyagents)
[![CI](https://github.com/skyforce77/tinyagents/actions/workflows/ci.yml/badge.svg)](https://github.com/skyforce77/tinyagents/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.24+-00ADD8.svg)](https://golang.org)

tinyagents is a Go library for composing LLM-powered agents into typed, supervised actor
systems. Agents communicate through bounded or unbounded mailboxes, route messages via
pluggable strategies, and restart under configurable supervision. Because each `agent.Agent`
embeds its own `llm.Provider` instance, one process can run Ollama, Anthropic, OpenAI, and
Mistral simultaneously — no global provider state, no singleton configuration.

Teams (`pkg/team`) compose individual agents into higher-order coordinators: Pipeline,
Broadcast, Router, Hierarchy, and Debate all speak the same `agent.Prompt` →
`agent.Response` interface, so they nest arbitrarily. Opt-in clustering over a gossip
protocol with CRDT state replication lets multiple tinyagents nodes form a ring, share
membership events, and converge distributed counters and sets without consensus. TCP
transport and a `tinyagentsctl` CLI round out the v0.1 surface.

---

## Features

### Actor core

- Goroutine-per-actor design; actors share no state and need no synchronization — `pkg/actor`
- Typed mailboxes: unbounded or bounded with Block / DropNewest / DropOldest / Fail backpressure — `pkg/mailbox`
- Supervision strategies: OneForOne restart with exponential backoff, Escalate, Stop — `pkg/supervisor`
- Five message-routing strategies: RoundRobin, Random, Broadcast, ConsistentHash, LeastLoaded — `pkg/router`
- Local PID registry + cluster-aware consistent-hash ring registry — `pkg/registry`
- Actor watch / lifecycle events for building watcher trees — `pkg/actor`

### LLM surface

- `llm.Provider` interface that every adapter satisfies; safe for concurrent use — `pkg/llm`
- Five adapters: OpenAI, Anthropic, Ollama, Mistral, OpenRouter — `pkg/llm/{openai,anthropic,ollama,mistral,openrouter}`
- Chainable middleware: Log, Retry, RateLimit, Fallback — `pkg/llm`
- Tool interface, `Func` adapter, and `Registry`; agents execute tool-call loops automatically — `pkg/tool`
- Ring-buffer conversation memory; pluggable backend via `memory.Memory` interface — `pkg/memory`, `pkg/memory/buffer`

### Team compositions

- Pipeline — output of each stage feeds the next stage's prompt — `pkg/team`
- Broadcast — fan out one prompt to N agents; collect all responses — `pkg/team`
- Router — stateful coordinator routes each prompt to one of N agents — `pkg/team`
- Hierarchy — a supervisor agent delegates sub-tasks to worker agents — `pkg/team`
- Debate — two debater agents exchange arguments; an arbiter decides — `pkg/team`

### Distributed mode

- Gossip-based cluster membership backed by a mature implementation — `pkg/cluster/memberlist`
- Local single-node cluster for tests and development — `pkg/cluster/local`
- GCounter, PNCounter, LWWRegister, ORSet CRDTs + `Replicator` for state sync — `pkg/crdt`
- TCP framing + wire codec for cross-node message delivery — `pkg/transport`

---

## Quickstart

```bash
go get github.com/skyforce77/tinyagents
```

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/skyforce77/tinyagents/pkg/actor"
    "github.com/skyforce77/tinyagents/pkg/agent"
    "github.com/skyforce77/tinyagents/pkg/llm"
    "github.com/skyforce77/tinyagents/pkg/llm/ollama"
    "github.com/skyforce77/tinyagents/pkg/memory/buffer"
)

func main() {
    provider, _ := ollama.New() // defaults to http://localhost:11434

    sys := actor.NewSystem("demo")
    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        _ = sys.Stop(ctx)
    }()

    ref, _ := sys.Spawn(agent.Spec("bot", agent.Agent{
        ID:       "bot",
        Provider: provider,
        Model:    "llama3.2",
        System:   "You are a concise assistant.",
        Memory:   buffer.New(16),
    }))

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    // Optional streaming: receive token deltas while Ask blocks for the Response.
    stream := make(chan llm.Chunk, 32)
    go func() {
        for chunk := range stream { fmt.Print(chunk.Delta) }
        fmt.Println()
    }()

    reply, _ := ref.Ask(ctx, agent.Prompt{Text: "Say hello in one sentence.", Stream: stream})
    if r, ok := reply.(agent.Response); ok {
        fmt.Println(r.Message.Content)
    }
}
```

---

## Heterogeneous providers

Each stage in a Pipeline independently selects its provider and model. The snippet below is
distilled from `examples/team-mixed-providers/main.go`:

```go
ollamaProv, _    := ollama.New()
anthropicProv    := anthropic.New(anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
openaiProv       := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))

sys := actor.NewSystem("team-mixed")

extractor, _  := sys.Spawn(agent.Spec("extractor", agent.Agent{
    ID: "extractor", Provider: ollamaProv, Model: "llama3.2",
    System: "Extract the 3-5 most important concepts as a comma-separated list.",
}))
summarizer, _ := sys.Spawn(agent.Spec("summarizer", agent.Agent{
    ID: "summarizer", Provider: anthropicProv, Model: "claude-sonnet-4-5-20250929",
    System: "Compose a dense paragraph explaining how the concepts relate.",
}))
formatter, _  := sys.Spawn(agent.Spec("formatter", agent.Agent{
    ID: "formatter", Provider: openaiProv, Model: "gpt-4o-mini",
    System: "Rewrite the paragraph as a single punchy title (max 12 words).",
}))

pipe, _ := sys.Spawn(team.Pipeline("mixed", extractor, summarizer, formatter))
reply, _ := pipe.Ask(ctx, agent.Prompt{Text: "Your prompt here."})
if r, ok := reply.(agent.Response); ok {
    fmt.Println(r.Message.Content)
}
```

---

## Clustering

tinyagents supports multi-node deployments over a gossip protocol. Nodes discover
each other through seed addresses and propagate membership events automatically.
The snippet below is from `examples/cluster-gossip/main.go`:

```go
alpha, _ := memberlist.New("alpha", "127.0.0.1:0")
beta,  _ := memberlist.New("beta",  "127.0.0.1:0")
gamma, _ := memberlist.New("gamma", "127.0.0.1:0")
defer alpha.Close(); defer beta.Close(); defer gamma.Close()

ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
_ = alpha.Join(ctx);  cancel() // bootstrap with no seeds

alphaAddr := alpha.LocalNode().Address // resolved after Join

ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
_ = beta.Join(ctx, alphaAddr); cancel()

ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
_ = gamma.Join(ctx, alphaAddr); cancel()

// All three nodes now see the full ring.
for _, m := range alpha.Members() {
    fmt.Printf("%s @ %s\n", m.ID, m.Address)
}
```

See [`docs/clustering.md`](docs/clustering.md) for the full guide including CRDT
state replication, actor registry sharding, and transport configuration.

---

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — system-level tour: how actor, agent, team, cluster, and CRDT packages compose
- [`docs/actor-model.md`](docs/actor-model.md) — `pkg/actor` deep dive: mailboxes, supervision, routers, registry
- [`docs/agents-and-teams.md`](docs/agents-and-teams.md) — `pkg/agent` and `pkg/team`: building, configuring, and nesting coordinators
- [`docs/clustering.md`](docs/clustering.md) — distributed mode: gossip membership, CRDT replication, cluster-aware registry
- [`docs/llm-providers.md`](docs/llm-providers.md) — `pkg/llm` adapter guide, middleware chains, tool-use loop

---

## Examples

All examples live under `examples/` and compile with `go build ./examples/<name>`.
Network calls are only made when the binary is executed.

- `hello/` — bare actor system: spawn a greeter, send two messages, stop
- `ask/` — request/reply with `ref.Ask` and typed responses
- `pipeline/` — chained actors demonstrating typed message passing through stages
- `agent-chat/` — single Ollama-backed agent with streaming output to stdout
- `team-pipeline/` — three-stage Ollama pipeline using `team.Pipeline`
- `team-mixed-providers/` — flagship: Ollama extract → Anthropic summarize → OpenAI format
- `team-debate/` — two debater agents plus an arbiter, all on Ollama
- `cluster-gossip/` — three-node gossip cluster: join, inspect membership, leave
- `crdt-counter/` — three PNCounter replicas converging over an in-process broadcast bus

---

## CLI

`tinyagentsctl` is the operator companion for tinyagents deployments.

```bash
tinyagentsctl version           # print version and build metadata
tinyagentsctl status [--addr]   # query the local node's health and uptime
tinyagentsctl members [--addr]  # list cluster members with ID, address, and meta
```

---

## Installation

Library:

```bash
go get github.com/skyforce77/tinyagents
```

CLI:

```bash
go install github.com/skyforce77/tinyagents/cmd/tinyagentsctl@latest
```

---

## Status / Roadmap

| Version | Scope |
|---------|-------|
| **v0.1** (current) | Actor core, LLM adapters, team coordinators, CRDT types + replicator, local and gossip cluster, TCP transport, CLI |
| v0.2 | Cluster-aware actor routing via transport; remote `ref.Ask` across nodes |
| v0.3 | TLS transport, durable actor state persistence, richer Prometheus metrics |

APIs are not yet stable; minor versions may introduce breaking changes until v1.0.

---

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Issues and pull requests are welcome;
every contribution must include passing tests and a clean `go vet ./...`.

---

## License

[AGPL-3.0](LICENSE) — strong copyleft: any network-accessible service incorporating
tinyagents must release its source under the same terms. Contact the project for
commercial or closed-source licensing alternatives.
