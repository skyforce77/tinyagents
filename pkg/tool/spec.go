package tool

import (
	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Spec converts a Tool into the neutral llm.ToolSpec used by Chat requests.
func Spec(t Tool) llm.ToolSpec {
	return llm.ToolSpec{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      t.Schema(),
	}
}

// Specs maps a slice of Tools. Useful when a caller has a []Tool at hand.
func Specs(tools []Tool) []llm.ToolSpec {
	out := make([]llm.ToolSpec, len(tools))
	for i, t := range tools {
		out[i] = Spec(t)
	}
	return out
}
