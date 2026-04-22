// team-mixed-providers demonstrates the core value proposition of
// tinyagents: a single Pipeline whose stages each run on a different LLM
// provider. Stage 1 runs on Ollama (local), stage 2 on Anthropic, stage
// 3 on OpenAI. Each Agent embeds its own Provider instance — no global
// state and no singleton — so heterogeneous teams coexist natively.
//
// Run (all three keys only needed if you actually want to exercise every
// stage; any stage without credentials is skipped with a warning):
//
//	export OLLAMA_HOST=http://localhost:11434
//	export ANTHROPIC_API_KEY=...
//	export OPENAI_API_KEY=...
//	go run ./examples/team-mixed-providers -prompt "Write a bedtime story about a Go gopher discovering distributed systems."
//
// CI only compiles this file; it does not execute.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm/anthropic"
	"github.com/skyforce77/tinyagents/pkg/llm/ollama"
	"github.com/skyforce77/tinyagents/pkg/llm/openai"
	"github.com/skyforce77/tinyagents/pkg/team"
)

func main() {
	prompt := flag.String("prompt", "Explain how gossip protocols help distributed systems stay coherent without consensus.", "user prompt")
	ollamaModel := flag.String("ollama-model", "llama3.2", "Ollama model for the extract stage")
	anthropicModel := flag.String("anthropic-model", "claude-sonnet-4-5-20250929", "Anthropic model for the summarize stage")
	openaiModel := flag.String("openai-model", "gpt-4o-mini", "OpenAI model for the format stage")
	timeout := flag.Duration("timeout", 2*time.Minute, "total deadline")
	flag.Parse()

	// Build three independent providers. Each Agent will embed exactly one
	// of them — the rest of the pipeline has no idea which provider any
	// given stage is using.
	var opts []ollama.Option
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		opts = append(opts, ollama.WithBaseURL(host))
	}
	ollamaProv, err := ollama.New(opts...)
	if err != nil {
		exitf("init ollama: %v", err)
	}

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		exitf("ANTHROPIC_API_KEY not set — this example runs three stages, set all three keys or pick a different example")
	}
	anthropicProv := anthropic.New(anthropic.WithAPIKey(anthropicKey))

	openaiKey := os.Getenv("OPENAI_API_KEY")
	if openaiKey == "" {
		exitf("OPENAI_API_KEY not set")
	}
	openaiProv := openai.New(openai.WithAPIKey(openaiKey))

	sys := actor.NewSystem("team-mixed")
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	}()

	// Stage 1: extractor on Ollama — cheap, local, finds salient keywords.
	extractor, err := sys.Spawn(agent.Spec("extractor", agent.Agent{
		ID:       "extractor",
		Provider: ollamaProv,
		Model:    *ollamaModel,
		System:   "Extract the 3-5 most important concepts from the user's text as a comma-separated list. Reply with ONLY the list.",
	}))
	if err != nil {
		exitf("spawn extractor: %v", err)
	}

	// Stage 2: summarizer on Anthropic — higher reasoning quality.
	summarizer, err := sys.Spawn(agent.Spec("summarizer", agent.Agent{
		ID:       "summarizer",
		Provider: anthropicProv,
		Model:    *anthropicModel,
		System:   "You receive a comma-separated list of concepts. Compose a single dense paragraph (3-4 sentences) that explains how they relate.",
	}))
	if err != nil {
		exitf("spawn summarizer: %v", err)
	}

	// Stage 3: formatter on OpenAI — turns the paragraph into a one-line title.
	formatter, err := sys.Spawn(agent.Spec("formatter", agent.Agent{
		ID:       "formatter",
		Provider: openaiProv,
		Model:    *openaiModel,
		System:   "Rewrite the user's paragraph as a single punchy title (max 12 words). Reply with just the title.",
	}))
	if err != nil {
		exitf("spawn formatter: %v", err)
	}

	pipe, err := sys.Spawn(team.Pipeline("mixed", extractor, summarizer, formatter))
	if err != nil {
		exitf("spawn pipeline: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	reply, err := pipe.Ask(ctx, agent.Prompt{Text: *prompt})
	if err != nil {
		exitf("ask: %v", err)
	}
	switch r := reply.(type) {
	case agent.Response:
		fmt.Println("=== final (from OpenAI formatter) ===")
		fmt.Println(r.Message.Content)
		if r.Usage != nil {
			fmt.Printf("[total usage] prompt=%d completion=%d total=%d\n",
				r.Usage.PromptTokens, r.Usage.CompletionTokens, r.Usage.TotalTokens)
		}
	case agent.Error:
		exitf("pipeline: %v", r.Err)
	default:
		exitf("unexpected reply type %T", reply)
	}
}

func exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
