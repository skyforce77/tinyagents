package openai

import (
	"context"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Embed posts to /embeddings and returns vectors in request order.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	resp, err := p.client.CreateEmbeddings(ctx, goopenai.EmbeddingRequest{
		Model: goopenai.EmbeddingModel(req.Model),
		Input: req.Input,
	})
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	vectors := make([][]float32, len(resp.Data))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			continue
		}
		vectors[d.Index] = d.Embedding
	}
	usage := toUsage(goopenai.Usage{
		PromptTokens: resp.Usage.PromptTokens,
		TotalTokens:  resp.Usage.TotalTokens,
	})
	return llm.EmbedResponse{Vectors: vectors, Usage: usage}, nil
}

// Models hits /models and turns the catalog into []llm.Model.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	resp, err := p.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		out = append(out, llm.Model{ID: m.ID})
	}
	return out, nil
}
