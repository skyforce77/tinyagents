package llm

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Registry maps provider names to Provider instances so callers can look
// them up by string (useful for configuration-driven agents). A Registry
// is optional; passing Provider values directly is always fine.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds or replaces p under p.Name().
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get returns the Provider registered under name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Names returns every registered provider name. Order is unspecified.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	return out
}

// Resolve splits "provider/model" and returns the Provider plus the model
// portion. An input of "openai/gpt-4o" with an "openai" registration yields
// (<openai>, "gpt-4o", nil). A plain "openai" resolves the provider with
// an empty model.
func (r *Registry) Resolve(fqn string) (Provider, string, error) {
	if fqn == "" {
		return nil, "", errors.New("llm: Resolve requires a non-empty name")
	}
	name, model, _ := strings.Cut(fqn, "/")
	p, ok := r.Get(name)
	if !ok {
		return nil, "", fmt.Errorf("llm: provider %q not registered", name)
	}
	return p, model, nil
}
