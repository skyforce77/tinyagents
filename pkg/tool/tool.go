// Package tool defines the callable-function abstraction that LLM agents
// expose to providers. A Tool is described by its name, description, and
// a JSON Schema for its arguments; Invoke executes the call.
package tool

import (
	"context"
	"encoding/json"
)

// Tool is a function an LLM agent can invoke on behalf of a model.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Invoke(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// Func adapts a plain function + metadata into a Tool, avoiding the
// boilerplate of a dedicated struct for simple cases.
type Func struct {
	ToolName   string
	ToolDesc   string
	ToolSchema json.RawMessage
	Fn         func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

func (f Func) Name() string                                                                    { return f.ToolName }
func (f Func) Description() string                                                             { return f.ToolDesc }
func (f Func) Schema() json.RawMessage                                                         { return f.ToolSchema }
func (f Func) Invoke(ctx context.Context, args json.RawMessage) (json.RawMessage, error)      { return f.Fn(ctx, args) }
