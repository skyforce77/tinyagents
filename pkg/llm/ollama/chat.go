package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// Chat issues a one-shot (stream=false) /api/chat call.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	body, err := p.buildChatBody(req, false)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return llm.ChatResponse{}, fmt.Errorf("ollama: chat http %d: %s", resp.StatusCode, string(payload))
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return llm.ChatResponse{}, fmt.Errorf("ollama: decode chat: %w", err)
	}
	return llm.ChatResponse{
		Message:      toMessage(cr.Message),
		FinishReason: cr.DoneReason,
		Usage:        usageFrom(cr.PromptEvalCount, cr.EvalCount),
	}, nil
}

// Stream issues /api/chat with stream=true and decodes NDJSON frames into
// a channel of llm.Chunk values. The channel closes after the terminal
// frame or on error. Callers should drain it or cancel ctx to abandon.
func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	body, err := p.buildChatBody(req, true)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama: stream http %d: %s", resp.StatusCode, string(payload))
	}

	ch := make(chan llm.Chunk, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var frame chatResponse
			if err := json.Unmarshal(line, &frame); err != nil {
				select {
				case ch <- llm.Chunk{FinishReason: "error"}:
				case <-ctx.Done():
				}
				return
			}
			chunk := llm.Chunk{Delta: frame.Message.Content}
			if len(frame.Message.ToolCalls) > 0 {
				// Ollama emits tool calls in one frame — expose each as its
				// own chunk so downstream code doesn't need to stitch them.
				for _, tc := range frame.Message.ToolCalls {
					call := tc // copy for closure/ref safety
					select {
					case ch <- llm.Chunk{ToolCall: &llm.ToolCall{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					}}:
					case <-ctx.Done():
						return
					}
				}
			}
			if frame.Done {
				chunk.FinishReason = frame.DoneReason
				chunk.Usage = usageFrom(frame.PromptEvalCount, frame.EvalCount)
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
			if frame.Done {
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
			select {
			case ch <- llm.Chunk{FinishReason: "error"}:
			case <-ctx.Done():
			}
		}
	}()
	return ch, nil
}

func (p *Provider) buildChatBody(req llm.ChatRequest, stream bool) ([]byte, error) {
	msgs := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := chatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, toolCall{
				Function: toolCallFunction{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		msgs = append(msgs, om)
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

	tools := make([]toolSpec, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, toolSpec{
			Type:     "function",
			Function: toolSpecFn{Name: t.Name, Description: t.Description, Parameters: t.Schema},
		})
	}

	payload := chatRequest{
		Model:    req.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   stream,
	}
	if len(opts) > 0 {
		payload.Options = opts
	}
	return json.Marshal(payload)
}

func toMessage(m chatMessage) llm.Message {
	out := llm.Message{
		Role:       llm.Role(m.Role),
		Content:    m.Content,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}

func usageFrom(promptTokens, completionTokens int) *llm.Usage {
	if promptTokens == 0 && completionTokens == 0 {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}
