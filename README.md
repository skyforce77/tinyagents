# tinyagents

A minimalist actor system for LLM agent orchestration in Go — each agent owns its provider.

`tinyagents` is a Go library for composing LLM-powered agents into typed,
supervised actor systems. Agents communicate through mailboxes and routers,
handle tool-use and streaming internally, and carry their own `llm.Provider`
instance — so one process can run Ollama, Anthropic, OpenAI, and Mistral
simultaneously in a single pipeline, with no global provider state and no
singleton configuration.

Module: `github.com/skyforce77/tinyagents` · Go 1.22+

---

## Features

- **Actor model** — goroutine-per-actor design with typed mailboxes (unbounded
  or bounded with Block/Drop policies), supervision trees (OneForOne, Restart
  with exponential backoff, Escalate, Stop), and a local PID registry.
- **Message routers** — RoundRobin, Random, Broadcast, ConsistentHash, and
  LeastLoaded strategies for fan-out and load distribution.
- **Streaming chat** — pass a `chan llm.Chunk` in `agent.Prompt` to receive
  token-by-token deltas while `Ask` blocks for the final `agent.Response`.
- **Tool-use loop** — `pkg/tool` provides a `Tool` interface, `Func` adapter,
  and `Registry`; agents execute tool calls and re-prompt automatically until
  the model is satisfied.
- **Per-agent token budget** — `agent.Policy` includes a `Budget` field;
  each agent enforces its own token limits independently of other agents.
- **Composable teams** — `pkg/team` exposes Pipeline, Broadcast, Router,
  Hierarchy, and Debate coordinators. Every coordinator speaks the same
  `agent.Prompt` → `agent.Response` interface, so teams nest arbitrarily.
- **Heterogeneous providers** — `pkg/llm` adapters for OpenAI, Anthropic,
  Ollama, Mistral, and OpenRouter. Each `agent.Agent` embeds its own
  `llm.Provider`; stages in the same pipeline can use entirely different
  backends and models.
- **LLM middleware** — chainable Log, Retry, RateLimit, and Fallback wrappers
  that compose over any `llm.Provider` via `pkg/llm`'s `Registry`.
- **Ring-buffer memory** — `pkg/memory/buffer` ships a fixed-capacity ring
  buffer for per-agent conversation history; the `pkg/memory` interface allows
  custom backends.
- **Roadmap to clustering** — M4 will add transport, cluster gossip, and CRDT
  state synchronization; M5 targets CLI tooling, polish, and full docs.

---

## Quickstart

Prerequisites:

```bash
go get github.com/skyforce77/tinyagents
```

Run a single Ollama-backed agent:

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/skyforce77/tinyagents/pkg/actor"
    "github.com/skyforce77/tinyagents/pkg/agent"
    "github.com/skyforce77/tinyagents/pkg/llm"
    "github.com/skyforce77/tinyagents/pkg/llm/ollama"
    "github.com/skyforce77/tinyagents/pkg/memory/buffer"
)

func main() {
    provider, _ := ollama.New()

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

    stream := make(chan llm.Chunk, 32)
    go func() {
        for chunk := range stream {
            fmt.Print(chunk.Delta)
        }
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

Each stage in a team pipeline independently selects its provider and model.
The example below trims `examples/team-mixed-providers/main.go` to the core
construction:

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

## Examples

All examples live under `examples/` and compile with `go build ./examples/<name>`.
Network calls are only made when the binary is executed.

- `hello/` — bare actor system: spawn, send a message, stop.
- `ask/` — request/reply with `ref.Ask` and typed responses.
- `pipeline/` — chained actors demonstrating message passing through stages.
- `agent-chat/` — single Ollama-backed agent with streaming output to stdout.
- `team-pipeline/` — three-stage Ollama pipeline using `team.Pipeline`.
- `team-mixed-providers/` — flagship demo: Ollama → Anthropic → OpenAI in one
  pipeline, each stage on a different provider.
- `team-debate/` — two debater agents plus an arbiter, all on Ollama.

---

## Status

**v0.1 — work in progress.** APIs are not yet stable.

| Milestone | Scope | State |
|-----------|-------|-------|
| M0 — actor core | `pkg/actor`, `pkg/mailbox` | shipped |
| M1 — supervisor + registry + router | `pkg/supervisor`, `pkg/registry`, `pkg/router` | shipped |
| M2 — LLM layer | `pkg/llm`, adapters, middleware, `pkg/tool`, `pkg/memory` | shipped |
| M3 — agent + team | `pkg/agent`, `pkg/team` (Pipeline/Broadcast/Router/Hierarchy/Debate) | shipped |
| M4 — distributed | transport, cluster gossip, CRDT state sync | upcoming |
| M5 — polish | CLI, benchmarks, full docs | upcoming |

Docs will arrive with M5.

---

## License

[AGPL-3.0](LICENSE) — GNU Affero General Public License v3.0.

This is a strong copyleft license: any network-accessible service that
incorporates `tinyagents` must release its source under the same terms.
If you need a commercial or closed-source arrangement, reach out to discuss
alternative licensing.
