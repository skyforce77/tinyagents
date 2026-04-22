# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

### Fixed

### Changed

---

## [0.1.0] — 2026-04-22

### Added

- **`pkg/actor`** — `Actor` interface, `ActorFunc` adapter, `Ref`, `PID`, `Context`,
  `Spec`, `System`, actor store, and watch/lifecycle events. Goroutine-per-actor runtime
  with typed mailboxes; actors share no state and require no synchronization.
- **`pkg/mailbox`** — `Mailbox` interface with unbounded and bounded implementations;
  four backpressure policies: Block, DropNewest, DropOldest, Fail.
- **`pkg/supervisor`** — `Strategy` interface; `Restart` strategy with exponential
  backoff and a configurable failure window; `Stop` and `Escalate` strategies.
- **`pkg/router`** — five routing strategies: RoundRobin, Random, Broadcast,
  ConsistentHash, LeastLoaded; `Router` actor wraps any strategy behind a `Ref`.
- **`pkg/registry`** — local in-memory PID registry; cluster-aware consistent-hash
  ring registry that routes lookups across gossip members.
- **`pkg/llm`** — `Provider` interface (`Chat`, `Stream`, `Embed`, `Models`);
  `Message`, `ChatRequest`, `ChatResponse`, `Chunk`, `Usage`, `ToolCall` types;
  `Registry` for named-provider lookup; `Middleware` wrapper type.
- **`pkg/llm/openai`** — OpenAI adapter (chat, streaming, embeddings).
- **`pkg/llm/anthropic`** — Anthropic adapter on `anthropic-sdk-go`.
- **`pkg/llm/ollama`** — Ollama adapter; configurable base URL via `WithBaseURL`.
- **`pkg/llm/mistral`** — Mistral adapter as a thin OpenAI-compatible wrapper.
- **`pkg/llm/openrouter`** — OpenRouter adapter for routing to hosted models.
- **`pkg/llm` middleware** — chainable Log, Retry (exponential backoff), RateLimit
  (token-bucket), and Fallback wrappers; each composes over any `llm.Provider`.
- **`pkg/tool`** — `Tool` interface, `Func` adapter for wrapping plain functions,
  `Spec` schema type, `Registry` for named-tool lookup.
- **`pkg/memory`** — `Memory` interface (`Append`, `Window`).
- **`pkg/memory/buffer`** — fixed-capacity ring-buffer implementation of `Memory`.
- **`pkg/agent`** — `Agent` actor: embeds `Provider`, `Memory`, `Policy`, and tool
  list; handles `Prompt` messages, executes tool-call loops, streams token deltas,
  and replies with `Response` or `Error`; `Spec` helper for supervised restarts.
- **`pkg/team`** — five coordinator patterns all speaking `agent.Prompt` →
  `agent.Response`: Pipeline (chained stages), Broadcast (fan-out/collect),
  Router (stateful dispatch), Hierarchy (delegating supervisor), Debate
  (two debaters + arbiter).
- **`pkg/cluster`** — `Cluster` interface (`Join`, `Leave`, `Members`, `Subscribe`,
  `Close`), `Member` struct, membership event types.
- **`pkg/cluster/local`** — single-node in-process cluster for tests.
- **`pkg/cluster/memberlist`** — gossip-based cluster backed by a mature
  distributed-systems membership library; supports ephemeral port binding.
- **`pkg/crdt`** — `CRDT` interface; GCounter, PNCounter, LWWRegister, ORSet
  implementations; `NodeID` type; `Replicator` with a pluggable `Broadcast` function
  for push-based delta propagation.
- **`pkg/transport`** — `Transport` interface; TCP framing with a length-prefixed
  wire codec and heartbeat support.
- **`internal/proto`** — `Envelope` wire type with serialization helpers.
- **`internal/wire`** — framing, codec, and heartbeat utilities for TCP transport.
- **`examples/hello`** — bare actor system: spawn, send, stop.
- **`examples/ask`** — request/reply with `ref.Ask` and typed responses.
- **`examples/pipeline`** — actor-level message-passing pipeline.
- **`examples/agent-chat`** — streaming Ollama-backed agent.
- **`examples/team-pipeline`** — three-stage Ollama pipeline via `team.Pipeline`.
- **`examples/team-mixed-providers`** — Ollama → Anthropic → OpenAI in one pipeline.
- **`examples/team-debate`** — two debaters + arbiter, all on Ollama.
- **`examples/cluster-gossip`** — three-node gossip cluster join/leave demo.
- **`examples/crdt-counter`** — PNCounter convergence over an in-process bus.
- **CLI `tinyagentsctl`** — `version`, `status`, and `members` subcommands.

### License

AGPL-3.0 — see [LICENSE](LICENSE).

---

[Unreleased]: https://github.com/skyforce77/tinyagents/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/skyforce77/tinyagents/releases/tag/v0.1.0
