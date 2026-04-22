package ollama

import (
	"context"
	"encoding/json"

	oapi "github.com/ollama/ollama/api"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Chat performs a non-streaming completion by accumulating the single
// ChatResponse delivered via the upstream callback.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	in, err := buildChatRequest(req, false)
	if err != nil {
		return llm.ChatResponse{}, err
	}

	var final oapi.ChatResponse
	cb := func(r oapi.ChatResponse) error {
		final = r
		return nil
	}
	if err := p.client.Chat(ctx, in, cb); err != nil {
		return llm.ChatResponse{}, err
	}
	return llm.ChatResponse{
		Message:      toMessage(final.Message),
		FinishReason: final.DoneReason,
		Usage:        usageFrom(final.Metrics),
	}, nil
}

// Stream performs a streaming completion, forwarding each callback frame
// as an llm.Chunk on the returned channel. The channel closes after the
// terminal frame or on error.
func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	in, err := buildChatRequest(req, true)
	if err != nil {
		return nil, err
	}

	ch := make(chan llm.Chunk, 8)
	go func() {
		defer close(ch)
		cb := func(r oapi.ChatResponse) error {
			chunk := llm.Chunk{Delta: r.Message.Content}
			if len(r.Message.ToolCalls) > 0 {
				for _, tc := range r.Message.ToolCalls {
					args, _ := json.Marshal(tc.Function.Arguments)
					select {
					case ch <- llm.Chunk{ToolCall: &llm.ToolCall{
						Name:      tc.Function.Name,
						Arguments: args,
					}}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			if r.Done {
				chunk.FinishReason = r.DoneReason
				chunk.Usage = usageFrom(r.Metrics)
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}
		_ = p.client.Chat(ctx, in, cb)
	}()
	return ch, nil
}

func buildChatRequest(req llm.ChatRequest, stream bool) (*oapi.ChatRequest, error) {
	msgs := make([]oapi.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := oapi.Message{
			Role:    string(m.Role),
			Content: m.Content,
		}
		for _, tc := range m.ToolCalls {
			var args oapi.ToolCallFunctionArguments
			if len(tc.Arguments) > 0 {
				if err := args.UnmarshalJSON(tc.Arguments); err != nil {
					return nil, err
				}
			}
			om.ToolCalls = append(om.ToolCalls, oapi.ToolCall{
				Function: oapi.ToolCallFunction{
					Name:      tc.Name,
					Arguments: args,
				},
			})
		}
		msgs = append(msgs, om)
	}

	tools := make([]oapi.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		var params oapi.ToolFunction
		// Ollama's ToolFunction expects the JSON schema inlined as its
		// Parameters field — we forward the raw bytes and let the upstream
		// client serialise.
		params.Name = t.Name
		params.Description = t.Description
		if len(t.Schema) > 0 {
			if err := json.Unmarshal(t.Schema, &params.Parameters); err != nil {
				return nil, err
			}
		}
		tools = append(tools, oapi.Tool{Type: "function", Function: params})
	}

	opts := map[string]any{}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		opts["num_predict"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		opts["stop"] = req.Stop
	}

	streamVal := stream
	return &oapi.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   &streamVal,
		Options:  opts,
	}, nil
}

func toMessage(m oapi.Message) llm.Message {
	out := llm.Message{
		Role:    llm.Role(m.Role),
		Content: m.Content,
	}
	for _, tc := range m.ToolCalls {
		args, _ := json.Marshal(tc.Function.Arguments)
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out
}

func usageFrom(m oapi.Metrics) *llm.Usage {
	if m.PromptEvalCount == 0 && m.EvalCount == 0 {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     m.PromptEvalCount,
		CompletionTokens: m.EvalCount,
		TotalTokens:      m.PromptEvalCount + m.EvalCount,
	}
}
