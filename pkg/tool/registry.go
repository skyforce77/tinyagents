package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var ErrUnknownTool = errors.New("tool: unknown tool")

// Registry is a concurrent-safe name → Tool map.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds or replaces t under t.Name().
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns the Tool registered under name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns every registered Tool. Order is unspecified.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Names returns every registered Tool name. Order is unspecified.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// Invoke looks up name and calls Invoke on the matching Tool. Returns
// ErrUnknownTool if the name isn't registered.
func (r *Registry) Invoke(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	t, ok := r.Get(name)
	if !ok {
		return nil, ErrUnknownTool
	}
	return t.Invoke(ctx, args)
}
