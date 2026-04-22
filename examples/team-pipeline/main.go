// team-pipeline demonstrates Pipeline composition on a single LLM provider.
// Three stateless agents (extractor, summarizer, formatter) all run on Ollama,
// forming a three-stage pipeline that transforms user text into a punchy title.
//
// Run:
//
//	go run ./examples/team-pipeline -prompt "Explain how CRDTs solve consistency in distributed systems without consensus."
//
// Override the Ollama endpoint with OLLAMA_HOST (defaults to http://localhost:11434).
// This contrasts with examples/team-mixed-providers, which uses three different providers.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/agent"
	"github.com/skyforce77/tinyagents/pkg/llm/ollama"
	"github.com/skyforce77/tinyagents/pkg/team"
)

func main() {
	prompt := flag.String("prompt", "Explain how CRDTs solve consistency in distributed systems without consensus.", "user prompt")
	model := flag.String("model", "llama3.2", "Ollama model used for all stages")
	timeout := flag.Duration("timeout", 2*time.Minute, "total deadline")
	flag.Parse()

	// Build a single Ollama provider, reused by all three agents.
	var opts []ollama.Option
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		opts = append(opts, ollama.WithBaseURL(host))
	}
	provider, err := ollama.New(opts...)
	if err != nil {
		exitf("init ollama: %v", err)
	}

	sys := actor.NewSystem("team-pipeline")
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	}()

	// Stage 1: extractor — finds 3–5 key concepts.
	extractor, err := sys.Spawn(agent.Spec("extractor", agent.Agent{
		ID:       "extractor",
		Provider: provider,
		Model:    *model,
		System:   "Extract the 3-5 most important concepts from the user's text as a comma-separated list. Reply with ONLY the list.",
	}))
	if err != nil {
		exitf("spawn extractor: %v", err)
	}

	// Stage 2: summarizer — explains how concepts relate.
	summarizer, err := sys.Spawn(agent.Spec("summarizer", agent.Agent{
		ID:       "summarizer",
		Provider: provider,
		Model:    *model,
		System:   "You receive a comma-separated list of concepts. Compose a single dense paragraph (3-4 sentences) that explains how they relate.",
	}))
	if err != nil {
		exitf("spawn summarizer: %v", err)
	}

	// Stage 3: formatter — turns the paragraph into a punchy title.
	formatter, err := sys.Spawn(agent.Spec("formatter", agent.Agent{
		ID:       "formatter",
		Provider: provider,
		Model:    *model,
		System:   "Rewrite the user's paragraph as a single punchy title (max 12 words). Reply with just the title.",
	}))
	if err != nil {
		exitf("spawn formatter: %v", err)
	}

	// Assemble the three-stage pipeline.
	pipe, err := sys.Spawn(team.Pipeline("pipe", extractor, summarizer, formatter))
	if err != nil {
		exitf("spawn pipeline: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Ask the pipeline (no streaming intermediate stages).
	reply, err := pipe.Ask(ctx, agent.Prompt{Text: *prompt})
	if err != nil {
		exitf("ask: %v", err)
	}

	switch r := reply.(type) {
	case agent.Response:
		fmt.Println("=== final title ===")
		fmt.Println(r.Message.Content)
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
