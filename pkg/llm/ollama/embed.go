package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

type wireEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type wireEmbedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float32 `json:"embeddings"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
}

type wireListResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// Embed calls /api/embed and returns dense vectors for every input string.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	body, err := json.Marshal(wireEmbedRequest{Model: req.Model, Input: req.Input})
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	resp, err := p.postJSON(ctx, "/api/embed", body)
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return llm.EmbedResponse{}, httpError(resp)
	}
	var out wireEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return llm.EmbedResponse{}, fmt.Errorf("ollama: decode embed response: %w", err)
	}
	var usage *llm.Usage
	if out.PromptEvalCount > 0 {
		usage = &llm.Usage{
			PromptTokens: out.PromptEvalCount,
			TotalTokens:  out.PromptEvalCount,
		}
	}
	return llm.EmbedResponse{Vectors: out.Embeddings, Usage: usage}, nil
}

// Models lists locally installed models via /api/tags.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, httpError(resp)
	}
	var out wireListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode models response: %w", err)
	}
	models := make([]llm.Model, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, llm.Model{ID: m.Name})
	}
	return models, nil
}
