// agent-chat spawns a single LLM agent on a local Ollama server and
// streams its reply to stdout as chunks arrive.
//
// Run:
//
//	go run ./examples/agent-chat -model llama3.2 -prompt "Say hi in one word"
//
// Override the Ollama endpoint with OLLAMA_HOST (defaults to the upstream
// default, http://localhost:11434). No API key is required; the example is
// safe to list in CI as `go build ./examples/agent-chat` — it only makes
// network calls when executed.
package main

import (
	"context"
	"flag"
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
	model := flag.String("model", "llama3.2", "Ollama model tag")
	prompt := flag.String("prompt", "Give me a one-sentence welcome message for a new Go library called tinyagents.", "user prompt")
	systemPrompt := flag.String("system", "You are a concise assistant. Reply in one short sentence.", "system prompt")
	timeout := flag.Duration("timeout", 60*time.Second, "total deadline for the exchange")
	flag.Parse()

	baseURL := os.Getenv("OLLAMA_HOST")
	var opts []ollama.Option
	if baseURL != "" {
		opts = append(opts, ollama.WithBaseURL(baseURL))
	}
	provider, err := ollama.New(opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init ollama:", err)
		os.Exit(1)
	}

	sys := actor.NewSystem("agent-chat")
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	}()

	ref, err := sys.Spawn(agent.Spec("chatter", agent.Agent{
		ID:       "chatter",
		Provider: provider,
		Model:    *model,
		System:   *systemPrompt,
		Memory:   buffer.New(16),
	}))
	if err != nil {
		fmt.Fprintln(os.Stderr, "spawn:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Stream chunks to stdout while Ask waits for the terminal Response.
	stream := make(chan llm.Chunk, 32)
	respCh := make(chan any, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, askErr := ref.Ask(ctx, agent.Prompt{Text: *prompt, Stream: stream})
		if askErr != nil {
			errCh <- askErr
			return
		}
		respCh <- resp
	}()

	fmt.Print("agent: ")
	for chunk := range stream {
		if chunk.Delta != "" {
			fmt.Print(chunk.Delta)
		}
	}
	fmt.Println()

	select {
	case resp := <-respCh:
		switch r := resp.(type) {
		case agent.Response:
			if r.Usage != nil {
				fmt.Printf("[usage] prompt=%d completion=%d total=%d\n",
					r.Usage.PromptTokens, r.Usage.CompletionTokens, r.Usage.TotalTokens)
			}
		case agent.Error:
			fmt.Fprintln(os.Stderr, "agent error:", r.Err)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "unexpected reply type %T\n", resp)
			os.Exit(1)
		}
	case err := <-errCh:
		fmt.Fprintln(os.Stderr, "ask:", err)
		os.Exit(1)
	}
}
