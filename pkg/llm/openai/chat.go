package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Chat issues a non-streaming completion.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	in, err := buildRequest(req, false)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	resp, err := p.client.CreateChatCompletion(ctx, *in)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		return llm.ChatResponse{}, errors.New("openai: chat returned no choices")
	}
	c := resp.Choices[0]
	return llm.ChatResponse{
		Message:      toMessage(c.Message),
		FinishReason: string(c.FinishReason),
		Usage:        toUsage(resp.Usage),
	}, nil
}

// Stream issues a streaming completion. Partial tool_call arguments are
// accumulated in-adapter so callers see one Chunk{ToolCall} per call.
func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	in, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	in.StreamOptions = &goopenai.StreamOptions{IncludeUsage: true}
	stream, err := p.client.CreateChatCompletionStream(ctx, *in)
	if err != nil {
		return nil, err
	}

	ch := make(chan llm.Chunk, 16)
	go func() {
		defer close(ch)
		defer stream.Close()
		toolBuf := map[int]*toolAccumulator{}
		for {
			resp, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				return
			}
			if rerr != nil {
				return
			}
			for _, choice := range resp.Choices {
				if choice.Delta.Content != "" {
					select {
					case ch <- llm.Chunk{Delta: choice.Delta.Content}:
					case <-ctx.Done():
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}
					acc, ok := toolBuf[idx]
					if !ok {
						acc = &toolAccumulator{}
						toolBuf[idx] = acc
					}
					if tc.ID != "" {
						acc.id = tc.ID
					}
					if tc.Function.Name != "" {
						acc.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						acc.args.WriteString(tc.Function.Arguments)
					}
				}
				if choice.FinishReason != "" {
					for _, k := range sortedKeys(toolBuf) {
						acc := toolBuf[k]
						select {
						case ch <- llm.Chunk{ToolCall: &llm.ToolCall{
							ID:        acc.id,
							Name:      acc.name,
							Arguments: json.RawMessage(acc.args.String()),
						}}:
						case <-ctx.Done():
							return
						}
					}
					toolBuf = map[int]*toolAccumulator{}
					select {
					case ch <- llm.Chunk{FinishReason: string(choice.FinishReason), Usage: toUsagePtr(resp.Usage)}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch, nil
}

type toolAccumulator struct {
	id   string
	name string
	args strings.Builder
}

func buildRequest(req llm.ChatRequest, stream bool) (*goopenai.ChatCompletionRequest, error) {
	msgs := make([]goopenai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := goopenai.ChatCompletionMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, goopenai.ToolCall{
				ID:   tc.ID,
				Type: goopenai.ToolTypeFunction,
				Function: goopenai.FunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		msgs = append(msgs, om)
	}

	tools := make([]goopenai.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		var params any = json.RawMessage(t.Schema)
		tools = append(tools, goopenai.Tool{
			Type: goopenai.ToolTypeFunction,
			Function: &goopenai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}

	out := &goopenai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   stream,
		Stop:     req.Stop,
	}
	if req.Temperature != nil {
		out.Temperature = float32(*req.Temperature)
	}
	if req.MaxTokens != nil {
		out.MaxCompletionTokens = *req.MaxTokens
	}
	if user, ok := req.Metadata["user"]; ok {
		out.User = user
	}
	return out, nil
}

func toMessage(m goopenai.ChatCompletionMessage) llm.Message {
	out := llm.Message{
		Role:       llm.Role(m.Role),
		Content:    m.Content,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return out
}

func toUsage(u goopenai.Usage) *llm.Usage {
	if u.TotalTokens == 0 {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

func toUsagePtr(u *goopenai.Usage) *llm.Usage {
	if u == nil {
		return nil
	}
	return toUsage(*u)
}

func sortedKeys(m map[int]*toolAccumulator) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
