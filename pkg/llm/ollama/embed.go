package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Embed calls /api/embed. Ollama returns one vector per input string in
// the same order.
func (p *Provider) Embed(ctx context.Context, req llm.EmbedRequest) (llm.EmbedResponse, error) {
	body, err := json.Marshal(embedRequest{Model: req.Model, Input: req.Input})
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return llm.EmbedResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return llm.EmbedResponse{}, fmt.Errorf("ollama: embed http %d: %s", resp.StatusCode, string(payload))
	}
	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return llm.EmbedResponse{}, fmt.Errorf("ollama: decode embed: %w", err)
	}
	return llm.EmbedResponse{
		Vectors: er.Embeddings,
		Usage:   usageFrom(er.PromptEvalCount, 0),
	}, nil
}

// Models calls /api/tags which lists locally installed models.
func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama: tags http %d: %s", resp.StatusCode, string(payload))
	}
	var tr tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("ollama: decode tags: %w", err)
	}
	out := make([]llm.Model, 0, len(tr.Models))
	for _, m := range tr.Models {
		out = append(out, llm.Model{ID: m.Name})
	}
	return out, nil
}
