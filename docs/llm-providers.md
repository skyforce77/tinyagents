# LLM Providers

This document covers `pkg/llm` in depth: the `Provider` interface, the
built-in adapters, the middleware stack, the Registry, streaming semantics,
tool calling, and a checklist for adding a new adapter.

See also:
[architecture.md](architecture.md) |
[actor-model.md](actor-model.md) |
[agents-and-teams.md](agents-and-teams.md) |
[clustering.md](clustering.md)

---

## Provider Interface

```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
    Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
    Models(ctx context.Context) ([]Model, error)
}
```

Every adapter satisfies this interface. Implementations must be safe for
concurrent use — a single instance can back many agents running in parallel.

- `Name()` returns a short identifier used in log lines and `Registry` lookups
  (e.g. `"openai"`, `"anthropic"`, `"ollama"`).
- `Chat` is the blocking round-trip. It returns the full response once the
  model has finished generating.
- `Stream` returns a channel of `Chunk` values. The channel is closed by the
  adapter after the terminal chunk. Callers must drain the channel or cancel
  the context to release the adapter's internal goroutines.
- `Embed` produces vector embeddings. The request and response shapes match
  the provider's embedding endpoint.
- `Models` lists available model identifiers. The catalog is typically cached
  by the adapter.

---

## Built-in Adapters

| Adapter | Backends it fits | Auth env var | Notes |
|---|---|---|---|
| `pkg/llm/openai` | OpenAI Chat Completions; wire-compatible backends: DeepSeek, Mistral openai-mode | `OPENAI_API_KEY` | Use `WithBaseURL` to point at a compatible endpoint; use `WithName` to rebrand in the Registry. |
| `pkg/llm/anthropic` | Anthropic Messages API | `ANTHROPIC_API_KEY` | Wraps the official `anthropic-sdk-go` client. Tool calling uses Anthropic's native format and is normalized to `llm.ToolCall` on output. |
| `pkg/llm/ollama` | Local Ollama server | `OLLAMA_HOST` (default `http://localhost:11434`) | Wraps the official `ollama/api` client. No API key required. Models served by Ollama are whatever you have pulled locally. |
| `pkg/llm/mistral` | Mistral AI (openai-mode endpoint) | `MISTRAL_API_KEY` | Thin wrapper over `pkg/llm/openai` with `WithBaseURL("https://api.mistral.ai/v1")` and `WithName("mistral")` pre-set. |
| `pkg/llm/openrouter` | OpenRouter (openai-mode endpoint) | `OPENROUTER_API_KEY` | Thin wrapper over `pkg/llm/openai` with `WithBaseURL("https://openrouter.ai/api/v1")` and `WithName("openrouter")` pre-set. |

### Construction examples

```go
import (
    "github.com/skyforce77/tinyagents/pkg/llm/openai"
    "github.com/skyforce77/tinyagents/pkg/llm/anthropic"
    "github.com/skyforce77/tinyagents/pkg/llm/ollama"
    "github.com/skyforce77/tinyagents/pkg/llm/mistral"
    "github.com/skyforce77/tinyagents/pkg/llm/openrouter"
)

oaiProv := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))

anthrProv := anthropic.New(anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))

ollamaProv, _ := ollama.New(
    ollama.WithBaseURL(os.Getenv("OLLAMA_HOST")), // optional override
)

mistralProv := mistral.New(os.Getenv("MISTRAL_API_KEY"))

orProv := openrouter.New(os.Getenv("OPENROUTER_API_KEY"))

// Using openai adapter against a wire-compatible backend:
deepSeekProv := openai.New(
    openai.WithAPIKey(os.Getenv("DEEPSEEK_API_KEY")),
    openai.WithBaseURL("https://api.deepseek.com/v1"),
    openai.WithName("deepseek"),
)
```

---

## Middleware

`Middleware` is a function that wraps a `Provider` and returns a new
`Provider`. The `With` combinator applies them in order (first listed is
outermost):

```go
type Middleware func(Provider) Provider

func With(base Provider, mws ...Middleware) Provider
```

### Built-in middleware

**`Log(logger *slog.Logger)`** — emits one slog line per call with operation
type, model, duration, token counts, and error status. A nil logger falls back
to `slog.Default()`.

**`Retry(maxAttempts int, backoff BackoffFunc, shouldRetry func(error) bool)`**
— retries `Chat` and `Embed` on error, up to `maxAttempts`. `Stream` and
`Models` are not retried (streams are stateful; model catalogs are usually
cached). `shouldRetry` filters which errors are worth retrying; nil means
"retry all errors".

**`RateLimit(interval time.Duration, burst int)`** — token-bucket rate limiter.
Allows `burst` requests immediately, then one every `interval`. Applies to all
four methods (`Chat`, `Stream`, `Embed`, `Models`). Blocks the caller until a
token is available or the context is cancelled.

**`Fallback(primary, backup Provider) Provider`** — not a `Middleware` but a
`Provider` combinator. On any non-nil error from `primary`, retries once
against `backup`. All four methods are covered.

### Example chain

```go
base := openai.New(openai.WithAPIKey(apiKey))

p := llm.With(base,
    llm.Log(slog.Default()),
    llm.Retry(3,
        llm.ExponentialBackoff(50*time.Millisecond, 2*time.Second),
        func(err error) bool {
            // Only retry on 5xx-class or network errors.
            var httpErr interface{ StatusCode() int }
            if errors.As(err, &httpErr) {
                return httpErr.StatusCode() >= 500
            }
            return true
        },
    ),
    llm.RateLimit(200*time.Millisecond, 5), // max 5 RPS burst
)

// For failover between two providers:
resilient := llm.Fallback(
    llm.With(openaiProv, llm.Retry(2, llm.ExponentialBackoff(100*time.Millisecond, time.Second), nil)),
    ollamaProv, // local fallback
)
```

Middleware wraps the `Provider` value, not the `*agent.Agent`. You can share
a single wrapped provider across many agents, or give each agent its own
independently configured chain.

---

## Registry

`llm.Registry` maps provider names to `Provider` instances for configuration-
driven setups where you want to resolve a provider by string rather than by
passing the value directly.

```go
reg := llm.NewRegistry()
reg.Register(openaiProv)
reg.Register(ollamaProv)

// Resolve by "provider/model":
prov, model, err := reg.Resolve("openai/gpt-4o-mini")
// prov == openaiProv, model == "gpt-4o-mini"

// Resolve provider only:
prov, _, err = reg.Resolve("ollama")
```

**When to use the Registry.** The Registry is useful when agent configuration
is loaded from a file or environment (e.g., `AGENT_MODEL=openai/gpt-4o-mini`)
and you want one place to look up the right provider. In code that directly
constructs agents, passing `Provider` values as plain function arguments is
cleaner.

---

## Streaming

`Provider.Stream` returns `<-chan Chunk`. Each `Chunk` carries:

```go
type Chunk struct {
    Delta        string     // incremental text to append
    ToolCall     *ToolCall  // set once per complete tool call; nil otherwise
    FinishReason string     // non-empty on the terminal chunk
    Usage        *Usage     // set on the terminal chunk for providers that report usage
}
```

Adapters normalize their provider-specific streaming formats to this shape:

- **OpenAI** uses Server-Sent Events with `data: {"choices": [{"delta": ...}]}`.
  The adapter reads from the SSE stream and emits one `Chunk` per SSE event.
  Tool call arguments that arrive fragmented across events are accumulated until
  a complete call can be emitted.

- **Anthropic** uses NDJSON-style Server-Sent Events with event types like
  `content_block_delta`. The adapter maps `text` deltas to `Chunk.Delta` and
  `tool_use` blocks to `Chunk.ToolCall`.

- **Ollama** uses a newline-delimited JSON stream. The adapter reads one JSON
  object per line and maps `response` to `Chunk.Delta`.

The caller reads from the channel until it closes:

```go
ch, err := prov.Stream(ctx, req)
if err != nil {
    return err
}
var buf strings.Builder
for chunk := range ch {
    buf.WriteString(chunk.Delta)
    if chunk.FinishReason != "" {
        fmt.Println("finish:", chunk.FinishReason)
    }
}
fmt.Println(buf.String())
```

Cancelling the context closes the channel and releases the adapter's internal
read goroutine. Do not abandon the channel without cancelling the context — the
goroutine will block on the SSE/NDJSON read until the connection is dropped.

---

## Tool Calling

Tool calling flows through three layers:

1. **`pkg/llm.ToolSpec`** describes a callable function to the provider. The
   `Schema` field is a JSON Schema `object` describing the arguments:

   ```go
   type ToolSpec struct {
       Name        string
       Description string
       Schema      json.RawMessage
   }
   ```

   `tool.Specs(tools []tool.Tool) []llm.ToolSpec` converts a slice of
   `tool.Tool` values into the provider-facing specs.

2. **Provider output.** When the model wants to call a function, the adapter
   normalizes the provider-specific format to `llm.ToolCall` on the assistant
   `Message`:

   ```go
   type ToolCall struct {
       ID        string
       Name      string
       Arguments json.RawMessage
   }
   ```

   `Message.ToolCalls` may contain multiple calls (the model requested several
   functions in one turn).

3. **`pkg/agent` invocation.** For each `ToolCall` in the assistant message,
   the Agent calls `tool.Tool.Invoke(ctx, args)` and formats the result as a
   `tool`-role `llm.Message` with `ToolCallID` set. This message is appended
   to the transcript and the provider is called again. See
   [agents-and-teams.md](agents-and-teams.md) for the full loop description.

---

## Adding a New Provider

Follow this checklist to add an adapter for a new backend.

1. **Create the package.** Add `pkg/llm/myprovider/myprovider.go`. The package
   doc should name the underlying SDK or HTTP API it wraps.

2. **Implement `llm.Provider`.** Define a `Provider` struct and implement all
   five methods: `Name`, `Chat`, `Stream`, `Embed`, `Models`. If the backend
   does not support embeddings, return `(EmbedResponse{}, errors.New("not supported"))`.

3. **Set `Name()` correctly.** The string must be stable and short — it becomes
   the key in `llm.Registry` lookups. Follow the convention: lowercase, no
   spaces (`"openai"`, `"anthropic"`, `"ollama"`).

4. **Marshal `ChatRequest` to the provider's wire format.** Map:
   - `req.Model` → model identifier
   - `req.Messages` → the provider's message array (translate `Role` values)
   - `req.Tools` → the provider's function/tool format (translate `ToolSpec`)
   - `req.Temperature`, `req.MaxTokens`, `req.Stop` → optional fields

5. **Emit `Chunk` from streams.** Wrap the provider's SSE, NDJSON, or callback
   stream in a goroutine that sends to a `chan Chunk`. Ensure the goroutine
   exits cleanly when the context is cancelled.

6. **Convert `ToolCall` correctly.** Tool calls may arrive fragmented across
   stream chunks. Accumulate argument bytes until you have a complete call
   before emitting a `Chunk{ToolCall: &llm.ToolCall{...}}`.

7. **Write tests using `httptest.Server`.** The `pkg/llm/ollama` package
   contains a reference test (`ollama_test.go`) that spins up an `httptest.Server`,
   registers fixture handlers for `/api/chat`, `/api/embed`, and `/api/tags`,
   and drives the adapter through every code path without network access. Copy
   that pattern.

8. **Register with the global type.** Add a constructor option `WithName` if
   the backend needs to be rebrand-able in the Registry (see the `openai`
   adapter for the pattern).

9. **Document the auth env var.** Add a row to the Built-in Adapters table in
   this document, and document the environment variable(s) used for API keys.

### Minimum viable adapter skeleton

```go
package myprovider

import (
    "context"
    "github.com/skyforce77/tinyagents/pkg/llm"
)

type Provider struct {
    // client ...
    name string
}

func New(apiKey string) *Provider {
    return &Provider{name: "myprovider"}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
    // 1. Translate req to provider wire format.
    // 2. POST to API.
    // 3. Parse response into llm.ChatResponse.
    return llm.ChatResponse{}, nil
}

func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
    ch := make(chan llm.Chunk, 16)
    go func() {
        defer close(ch)
        // 1. Open SSE/NDJSON stream.
        // 2. For each frame: parse and send ch <- llm.Chunk{...}.
        // 3. On context cancellation: return.
    }()
    return ch, nil
}

func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
    return llm.EmbedResponse{}, errors.New("myprovider: embed not supported")
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
    return nil, nil
}

// Compile-time check.
var _ llm.Provider = (*Provider)(nil)
```

The compile-time check at the bottom ensures that a missing method causes a
build error rather than a runtime panic.
