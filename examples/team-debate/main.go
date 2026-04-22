// team-debate demonstrates Debate with two debaters and an arbiter, all backed
// by Ollama. Pro and con debaters alternate for N rounds, then the arbiter
// reads the full transcript to declare a winner.
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
	topic := flag.String("topic", "Should distributed systems prefer strong consistency over availability?", "debate topic")
	rounds := flag.Int("rounds", 2, "number of debate rounds")
	model := flag.String("model", "llama3.2", "Ollama model for all agents")
	timeout := flag.Duration("timeout", 3*time.Minute, "total deadline")
	flag.Parse()

	// Create a single Ollama provider, reused for all three agents.
	var opts []ollama.Option
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		opts = append(opts, ollama.WithBaseURL(host))
	}
	provider, err := ollama.New(opts...)
	if err != nil {
		exitf("init ollama: %v", err)
	}

	sys := actor.NewSystem("team-debate")
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	}()

	// Spawn pro debater
	pro, err := sys.Spawn(agent.Spec("pro", agent.Agent{
		ID:       "pro",
		Provider: provider,
		Model:    *model,
		System:   "You argue the PRO side of the topic. Be concise (2-3 sentences per turn) and persuasive. Address the previous arguments directly.",
	}))
	if err != nil {
		exitf("spawn pro: %v", err)
	}

	// Spawn con debater
	con, err := sys.Spawn(agent.Spec("con", agent.Agent{
		ID:       "con",
		Provider: provider,
		Model:    *model,
		System:   "You argue the CON side of the topic. Be concise (2-3 sentences per turn) and persuasive. Address the previous arguments directly.",
	}))
	if err != nil {
		exitf("spawn con: %v", err)
	}

	// Spawn arbiter
	arbiter, err := sys.Spawn(agent.Spec("arbiter", agent.Agent{
		ID:       "arbiter",
		Provider: provider,
		Model:    *model,
		System:   "You are a neutral arbiter. Read the full debate transcript and declare which side presented the stronger arguments, with a brief justification (3-4 sentences).",
	}))
	if err != nil {
		exitf("spawn arbiter: %v", err)
	}

	// Spawn debate team
	debate, err := sys.Spawn(team.Debate("debate", *rounds, arbiter, pro, con))
	if err != nil {
		exitf("spawn debate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	reply, err := debate.Ask(ctx, agent.Prompt{Text: *topic})
	if err != nil {
		exitf("ask: %v", err)
	}
	switch r := reply.(type) {
	case agent.Response:
		fmt.Println("=== arbiter verdict ===")
		fmt.Println(r.Message.Content)
	case agent.Error:
		exitf("debate: %v", r.Err)
	default:
		exitf("unexpected reply type %T", reply)
	}
}

func exitf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
