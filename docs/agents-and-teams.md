# Agents and Teams

This document covers `pkg/agent` and `pkg/team` in depth: agent anatomy,
message shapes, streaming, the tool-use loop, budget tracking, and all five
team coordination patterns. It assumes familiarity with `pkg/actor`
(see [actor-model.md](actor-model.md)).

See also:
[architecture.md](architecture.md) |
[actor-model.md](actor-model.md) |
[clustering.md](clustering.md) |
[llm-providers.md](llm-providers.md)

---

## Agent Anatomy

`agent.Agent` implements `actor.Actor`. Spawn it with `agent.Spec`:

```go
import (
    "github.com/skyforce77/tinyagents/pkg/actor"
    "github.com/skyforce77/tinyagents/pkg/agent"
    "github.com/skyforce77/tinyagents/pkg/llm/ollama"
    "github.com/skyforce77/tinyagents/pkg/tool"
)

prov, _ := ollama.New()

ref, err := sys.Spawn(agent.Spec("assistant", agent.Agent{
    ID:       "assistant",
    Provider: prov,
    Model:    "llama3.2",
    System:   "You are a helpful assistant.",
    Tools:    []tool.Tool{myTool},
    Memory:   myMemory, // nil = ephemeral ring per prompt
    Policy: agent.Policy{
        MaxTurns:    8,
        Temperature: floatPtr(0.7),
        MaxTokens:   2048,
        Budget:      sharedBudget,
    },
}))
```

Each field:

| Field | Type | Purpose |
|---|---|---|
| `ID` | `string` | Human-readable label for logs and errors. Does not need to be unique — use the PID for that. |
| `Provider` | `llm.Provider` | The LLM backend for every call this agent makes. Required. |
| `Model` | `string` | Forwarded as `ChatRequest.Model`; interpretation is adapter-specific (`"gpt-4o-mini"`, `"claude-sonnet-4-5"`, `"llama3.2:latest"`). |
| `System` | `string` | Optional system-role prompt prepended to every provider request. |
| `Tools` | `[]tool.Tool` | Functions exposed to the model. When the assistant emits tool calls, the Agent invokes them and loops. |
| `Memory` | `memory.Memory` | Conversation transcript store across prompts. Nil means a fresh ephemeral ring buffer is created per prompt — tool-use loops within one prompt still have context, but nothing persists between prompts. |
| `Policy` | `agent.Policy` | Bounds on token usage, turn depth, temperature, and max tokens. All fields are optional. |

### Policy fields

| Field | Default | Purpose |
|---|---|---|
| `MaxTurns` | `5` (`DefaultMaxTurns`) | Maximum assistant→tool→assistant iterations per prompt. Set to `1` to disable the tool-use loop entirely. |
| `Temperature` | nil (provider default) | Forwarded to `ChatRequest.Temperature`. |
| `MaxTokens` | 0 (provider default) | Forwarded to `ChatRequest.MaxTokens` when > 0. |
| `Budget` | nil (no enforcement) | Shared token counter. See the Budget section below. |

---

## Message Shapes

An agent handles exactly one message type: `agent.Prompt`. It replies with
`agent.Response` on success or `agent.Error` on failure.

### agent.Prompt

```go
type Prompt struct {
    Text   string
    Role   llm.Role       // defaults to llm.RoleUser
    Stream chan<- llm.Chunk // nil = blocking Chat call
}
```

`Text` becomes the content of a message appended to the agent's transcript.
`Role` lets callers inject non-user messages (e.g. `llm.RoleSystem` for
out-of-band observations). `Stream`, when non-nil, enables streaming mode —
see the Streaming section below.

### agent.Response

```go
type Response struct {
    Message llm.Message // the final assistant message (no pending ToolCalls)
    Usage   *llm.Usage  // sum across all provider calls for this Prompt; nil if unreported
}
```

### agent.Error

```go
type Error struct{ Err error }
func (e Error) Error() string { return e.Err.Error() }
```

`Error` is returned instead of `Response` when the agent cannot complete the
prompt: budget exhausted, provider error, unknown tool name, or `MaxTurns`
exceeded without a final answer.

### Sending a prompt

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

raw, err := ref.Ask(ctx, agent.Prompt{Text: "Summarize the following text..."})
if err != nil {
    // actor/transport-level error
}
switch r := raw.(type) {
case agent.Response:
    fmt.Println(r.Message.Content)
case agent.Error:
    fmt.Fprintf(os.Stderr, "agent error: %v\n", r.Err)
}
```

---

## Streaming

Pass a `chan llm.Chunk` in `Prompt.Stream` to receive incremental output:

```go
chunks := make(chan llm.Chunk, 32)
go func() {
    for chunk := range chunks {
        if chunk.Delta != "" {
            fmt.Print(chunk.Delta)
        }
    }
    fmt.Println() // newline after stream closes
}()

raw, err := ref.Ask(ctx, agent.Prompt{
    Text:   "Tell me a story.",
    Stream: chunks,
})
```

The agent closes `Stream` before sending the final `Response`. This means you
can safely `range` over the channel and then read from the `Ask` result — the
range will finish, and then the reply will be available.

Streaming applies across the entire tool-use loop. If the model calls a tool
and then continues generating, the chunks from the second provider call are also
forwarded to the same `Stream` channel. The final `Response.Message` is the
fully assembled assistant message.

---

## Tool-Use Loop

When the agent's provider returns a message that contains `ToolCalls`, the
agent does not immediately reply to the sender. Instead:

1. The assistant message (with its tool calls) is appended to `Memory`.
2. For each `ToolCall`, the agent looks up the matching `tool.Tool` by name in
   its `Tools` slice and calls `Invoke(ctx, args)`.
3. Each result is formatted as a `tool`-role `llm.Message` and appended to
   `Memory`.
4. The agent builds a new `ChatRequest` from the updated transcript and calls
   the provider again. This is one turn.
5. The loop repeats until either the provider returns a message with no tool
   calls (the final answer), or `Policy.MaxTurns` is exhausted.

Unknown tool names and invocation errors are surfaced inside the `Content`
string of the tool-role message rather than as hard failures. This gives the
model a chance to recover on the next turn.

```go
// A minimal tool using tool.Func:
calcTool := tool.Func{
    ToolName: "calculator",
    ToolDesc: "Evaluates a simple arithmetic expression.",
    ToolSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "expression": {"type": "string"}
        },
        "required": ["expression"]
    }`),
    Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
        var req struct{ Expression string `json:"expression"` }
        json.Unmarshal(args, &req)
        result := evaluate(req.Expression) // your implementation
        return json.Marshal(map[string]any{"result": result})
    },
}

ref, _ := sys.Spawn(agent.Spec("calc-agent", agent.Agent{
    Provider: prov,
    Model:    "gpt-4o-mini",
    Tools:    []tool.Tool{calcTool},
    Policy:   agent.Policy{MaxTurns: 5},
}))
```

---

## Budget

`agent.Budget` is a concurrent-safe running token total. It is opt-in: an
agent without `Policy.Budget` consumes tokens without accounting.

```go
budget := &agent.Budget{MaxTokens: 10_000}

// Shared by two agents — the ceiling applies to combined consumption.
ref1, _ := sys.Spawn(agent.Spec("a1", agent.Agent{
    Provider: prov, Model: "gpt-4o-mini",
    Policy: agent.Policy{Budget: budget},
}))
ref2, _ := sys.Spawn(agent.Spec("a2", agent.Agent{
    Provider: prov, Model: "gpt-4o-mini",
    Policy: agent.Policy{Budget: budget},
}))

fmt.Println(budget.Used())      // running total
fmt.Println(budget.Remaining()) // 10000 - used
```

Before each provider call, `Budget.Reserve()` is checked; after the call,
`Budget.Record(usage)` is called. If either call returns `ErrBudgetExceeded`,
the agent sends an `agent.Error` back to the sender and stops processing the
current prompt. The actor is not restarted — budget exhaustion is a domain
event, not a crash.

**Per-agent budget.** Give each agent its own `Budget` to enforce an
individual ceiling.

**Team-wide budget.** Pass the same `*Budget` pointer to every member of a
team. All members draw from the same pool, so the first one to exhaust it
causes its prompt to fail while others may still succeed.

**Observability without gating.** Set `MaxTokens: 0` to accumulate usage
without enforcing a ceiling. `Budget.Used()` is readable from any goroutine.

---

## Teams

`pkg/team` provides five coordinator patterns that each expose the same
`agent.Prompt` / `agent.Response` / `agent.Error` surface as a single agent.
Because every coordinator is itself an actor, teams nest: a `Pipeline` can
contain a `Broadcast`, which can contain individual agents, all without any
coordinator knowing about the others' internals.

Every coordinator function returns an `actor.Spec`, so spawning is explicit:

```go
pipe, err := sys.Spawn(team.Pipeline("stage-pipe", stage1, stage2, stage3))
```

### Pipeline

```go
func Pipeline(name string, stages ...actor.Ref) actor.Spec
```

Feeds each stage's `Response.Message.Content` as a fresh user `Prompt` to the
next stage. Usage is summed across all stages.

**Streaming.** Only the final stage receives `Prompt.Stream`. Intermediate
stages run non-streaming to avoid exposing intermediate reasoning to the caller.

**Failure.** If any stage returns `agent.Error` or the `Ask` itself fails
(e.g., actor stopped), the coordinator immediately returns `agent.Error` to the
original sender and does not invoke subsequent stages.

**When to use.** Progressive refinement: draft → review → format; keyword
extraction → elaboration → summarization; translation → quality check.

```go
extractor, _ := sys.Spawn(agent.Spec("extractor", agent.Agent{
    Provider: ollamaProv, Model: "llama3.2",
    System: "Extract the 3-5 most important concepts as a comma-separated list.",
}))
writer, _ := sys.Spawn(agent.Spec("writer", agent.Agent{
    Provider: anthropicProv, Model: "claude-sonnet-4-5-20250929",
    System: "Given a comma-separated list of concepts, write a short explanatory paragraph.",
}))
formatter, _ := sys.Spawn(agent.Spec("formatter", agent.Agent{
    Provider: openaiProv, Model: "gpt-4o-mini",
    System: "Rewrite the paragraph as a single punchy title (max 12 words).",
}))

pipe, _ := sys.Spawn(team.Pipeline("mixed", extractor, writer, formatter))
```

See `examples/team-mixed-providers` for the full runnable version.

### Broadcast

```go
func Broadcast(name string, members ...actor.Ref) actor.Spec
```

Fans the prompt out to every member in parallel, then joins all responses in
order: `parts[0] + "\n---\n" + parts[1] + ...`.

**Streaming.** Not supported in v0.1. A `Prompt.Stream` is rejected with
`agent.Error` and the channel is closed.

**Failure (short-circuit).** On the first member error, outstanding `Ask`s are
cancelled via context and `agent.Error` is returned. No partial aggregation.

**When to use.** Gather multiple perspectives on the same question; multi-judge
evaluation; redundant fact-checking.

```go
broadcast, _ := sys.Spawn(team.Broadcast("judges",
    judge1Ref, judge2Ref, judge3Ref,
))
raw, _ := broadcast.Ask(ctx, agent.Prompt{Text: "Evaluate this contract clause: ..."})
// Response.Message.Content is the three verdicts joined by "\n---\n"
```

### Router

```go
func Router(name string, keyFn KeyFunc, members ...actor.Ref) actor.Spec
```

Dispatches each prompt to exactly one member using stable key-based hashing
(`FNV-64a(key) % len(members)`). The mapping is stable within a process but
is not consistent hashing for clusters — a change in `len(members)` reassigns
keys.

**Streaming.** Forwarded directly to the chosen member; the member closes the
channel.

**Failure.** Member error is wrapped and returned as `agent.Error`.

**When to use.** Route by conversation session ID, user ID, or document
namespace so that per-key in-memory state stays on one agent.

```go
byLang := team.Router("lang-router",
    func(p agent.Prompt) string { return detectLanguage(p.Text) },
    englishAgent, frenchAgent, germanAgent,
)
langRef, _ := sys.Spawn(byLang)
```

### Hierarchy

```go
func Hierarchy(name string, lead actor.Ref, workers ...actor.Ref) actor.Spec
```

Phase 1: fans the prompt to all workers in parallel (non-streaming). Phase 2:
builds a synthesis prompt — original question + all worker replies — and asks
the lead agent to produce a single coherent answer.

**Streaming.** Only the lead's final call receives `Prompt.Stream`.

**Failure.** Any worker error short-circuits before the lead is consulted.

**When to use.** Expert panel → lead summarizer; parallel research → synthesis;
multi-model voting where one model has final authority.

```go
lead, _ := sys.Spawn(agent.Spec("lead", agent.Agent{
    Provider: anthropicProv, Model: "claude-opus-4-5",
    System: "You synthesize expert opinions into a clear answer.",
}))
w1, _ := sys.Spawn(agent.Spec("legal-expert", agent.Agent{...}))
w2, _ := sys.Spawn(agent.Spec("tech-expert", agent.Agent{...}))
w3, _ := sys.Spawn(agent.Spec("econ-expert", agent.Agent{...}))

hier, _ := sys.Spawn(team.Hierarchy("panel", lead, w1, w2, w3))
```

### Debate

```go
func Debate(name string, rounds int, arbiter actor.Ref, debaters ...actor.Ref) actor.Spec
```

Runs `rounds` full round-robin turns between the debaters. Each debater
receives the growing transcript (starting from the original topic) and appends
its reply. After all rounds, the arbiter reads the complete transcript and
declares a verdict.

**Streaming.** Only the arbiter's final call receives `Prompt.Stream`.

**Failure.** Any debater error short-circuits; the arbiter is not consulted.

**Constraints.** At least two debaters; at least one round; arbiter must be
non-nil. Panics on invalid configuration — validate before calling `Debate`.

**When to use.** Structured argument exploration; multi-perspective stress
testing; red-team / blue-team analysis.

```go
pro, _ := sys.Spawn(agent.Spec("pro", agent.Agent{
    Provider: prov, Model: "llama3.2",
    System: "You argue strongly FOR the proposition.",
}))
con, _ := sys.Spawn(agent.Spec("con", agent.Agent{
    Provider: prov, Model: "llama3.2",
    System: "You argue strongly AGAINST the proposition.",
}))
arbiter, _ := sys.Spawn(agent.Spec("arbiter", agent.Agent{
    Provider: prov, Model: "llama3.2",
    System: "You are a neutral arbiter. Analyze the debate and declare a verdict.",
}))

debate, _ := sys.Spawn(team.Debate("ethics-debate", 3, arbiter, pro, con))
raw, _ := debate.Ask(ctx, agent.Prompt{
    Text: "AI systems should have legal personhood.",
})
```

See `examples/team-debate` for the full runnable version.

---

## Heterogeneous Providers

Because each `agent.Agent` holds its own `Provider`, a single team can mix
provider backends freely. The coordinator code has zero knowledge of which
provider any given member uses.

The `examples/team-mixed-providers` example shows a three-stage pipeline where
stage 1 runs on Ollama, stage 2 on Anthropic, and stage 3 on OpenAI. The
distilled core:

```go
ollamaProv, _ := ollama.New()
anthropicProv := anthropic.New(anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
openaiProv := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))

extractor, _ := sys.Spawn(agent.Spec("extractor", agent.Agent{
    Provider: ollamaProv, Model: "llama3.2",
    System: "Extract the 3-5 most important concepts as a comma-separated list.",
}))
summarizer, _ := sys.Spawn(agent.Spec("summarizer", agent.Agent{
    Provider: anthropicProv, Model: "claude-sonnet-4-5-20250929",
    System: "Write a dense paragraph explaining how the concepts relate.",
}))
formatter, _ := sys.Spawn(agent.Spec("formatter", agent.Agent{
    Provider: openaiProv, Model: "gpt-4o-mini",
    System: "Rewrite as a single punchy title (max 12 words).",
}))

pipe, _ := sys.Spawn(team.Pipeline("mixed", extractor, summarizer, formatter))

raw, _ := pipe.Ask(ctx, agent.Prompt{Text: *prompt})
switch r := raw.(type) {
case agent.Response:
    fmt.Println(r.Message.Content)
case agent.Error:
    fmt.Fprintln(os.Stderr, r.Err)
}
```

The same nesting principle applies to every team pattern. A `Hierarchy` whose
workers are themselves `Broadcast` coordinators, each backed by a different
provider, is valid and requires no changes to any coordinator code.
