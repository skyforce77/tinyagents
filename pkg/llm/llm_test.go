package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// stubProvider is the smallest valid Provider used to exercise the registry.
type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("not implemented")
}
func (s stubProvider) Stream(context.Context, llm.ChatRequest) (<-chan llm.Chunk, error) {
	return nil, errors.New("not implemented")
}
func (s stubProvider) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, errors.New("not implemented")
}
func (s stubProvider) Models(context.Context) ([]llm.Model, error) {
	return nil, errors.New("not implemented")
}

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()
	r := llm.NewRegistry()
	a := stubProvider{name: "alpha"}
	r.Register(a)

	got, ok := r.Get("alpha")
	if !ok {
		t.Fatal("Get alpha missing")
	}
	if got.Name() != "alpha" {
		t.Fatalf("Name = %q, want alpha", got.Name())
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get missing returned ok")
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Parallel()
	r := llm.NewRegistry()
	r.Register(stubProvider{name: "openai"})

	p, model, err := r.Resolve("openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openai" {
		t.Fatalf("provider name = %q, want openai", p.Name())
	}
	if model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", model)
	}

	// Bare provider name yields empty model.
	_, model, err = r.Resolve("openai")
	if err != nil {
		t.Fatal(err)
	}
	if model != "" {
		t.Fatalf("bare name model = %q, want empty", model)
	}

	// Unknown provider is an error.
	if _, _, err := r.Resolve("mystery/x"); err == nil {
		t.Fatal("expected error for unknown provider")
	}

	// Empty is also an error.
	if _, _, err := r.Resolve(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestRegistryNames(t *testing.T) {
	t.Parallel()
	r := llm.NewRegistry()
	r.Register(stubProvider{name: "alpha"})
	r.Register(stubProvider{name: "beta"})

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if !set["alpha"] || !set["beta"] {
		t.Fatalf("names missing entries: %v", names)
	}
}
