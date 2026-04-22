package anthropic

import (
	"context"
	"fmt"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Embed always returns an error because Anthropic does not offer an embeddings
// endpoint.
func (p *Provider) Embed(_ context.Context, _ llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, fmt.Errorf("anthropic: embeddings not supported")
}

// Models returns an empty slice because the Anthropic SDK does not expose a
// models list endpoint in this version.
func (p *Provider) Models(_ context.Context) ([]llm.Model, error) {
	return nil, nil
}
