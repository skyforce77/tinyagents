package ollama

import (
	"context"

	oapi "github.com/ollama/ollama/api"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Embed calls /api/embed via the upstream client.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	resp, err := p.client.Embed(ctx, &oapi.EmbedRequest{
		Model: req.Model,
		Input: req.Input,
	})
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	var usage *llm.Usage
	if resp.PromptEvalCount > 0 {
		usage = &llm.Usage{
			PromptTokens: resp.PromptEvalCount,
			TotalTokens:  resp.PromptEvalCount,
		}
	}
	return llm.EmbedResponse{Vectors: resp.Embeddings, Usage: usage}, nil
}

// Models lists locally installed models via /api/tags.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	resp, err := p.client.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		out = append(out, llm.Model{ID: m.Name})
	}
	return out, nil
}
