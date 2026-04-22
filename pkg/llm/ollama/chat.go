package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// wireMessage is the subset of the Ollama chat message payload this adapter
// exchanges. Content is free-form text; ToolCalls carries function-call
// requests on assistant frames. Arguments is kept as json.RawMessage so the
// adapter never re-serializes the provider's bytes.
type wireMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
}

type wireToolCall struct {
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function,omitempty"`
}

type wireToolSpec struct {
	Type     string               `json:"type"`
	Function wireToolSpecFunction `json:"function"`
}

type wireToolSpecFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireChatRequest struct {
	Model    string         `json:"model"`
	Messages []wireMessage  `json:"messages"`
	Tools    []wireToolSpec `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type wireChatResponse struct {
	Model           string      `json:"model"`
	Message         wireMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason,omitempty"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
	EvalCount       int         `json:"eval_count,omitempty"`
}

// Chat performs a non-streaming completion. Ollama returns a single JSON
// object on /api/chat when stream is false.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	body, err := buildChatRequest(req, false)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	resp, err := p.postJSON(ctx, "/api/chat", body)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return llm.ChatResponse{}, httpError(resp)
	}
	var out wireChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return llm.ChatResponse{}, fmt.Errorf("ollama: decode chat response: %w", err)
	}
	return llm.ChatResponse{
		Message:      toMessage(out.Message),
		FinishReason: out.DoneReason,
		Usage:        usageFrom(out.PromptEvalCount, out.EvalCount),
	}, nil
}

// Stream performs a streaming completion and forwards each NDJSON frame as
// an llm.Chunk. The returned channel closes after the terminal frame or on
// error.
func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	body, err := buildChatRequest(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.postJSON(ctx, "/api/chat", body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		err := httpError(resp)
		resp.Body.Close()
		return nil, err
	}

	ch := make(chan llm.Chunk, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var frame wireChatResponse
			if jerr := json.Unmarshal(line, &frame); jerr != nil {
				return
			}
			chunk := llm.Chunk{Delta: frame.Message.Content}
			for _, tc := range frame.Message.ToolCalls {
				select {
				case ch <- llm.Chunk{ToolCall: &llm.ToolCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}}:
				case <-ctx.Done():
					return
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
		}
	}()
	return ch, nil
}

func buildChatRequest(req llm.ChatRequest, stream bool) ([]byte, error) {
	msgs := make([]wireMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role), Content: m.Content}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				Function: wireToolFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		msgs = append(msgs, wm)
	}

	tools := make([]wireToolSpec, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, wireToolSpec{
			Type: "function",
			Function: wireToolSpecFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
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

	payload := wireChatRequest{
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

func toMessage(m wireMessage) llm.Message {
	out := llm.Message{Role: llm.Role(m.Role), Content: m.Content}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}

func usageFrom(prompt, completion int) *llm.Usage {
	if prompt == 0 && completion == 0 {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// postJSON marshals body as JSON and POSTs it to p.baseURL + path.
func (p *Provider) postJSON(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.http.Do(req)
}

// httpError builds a concise error from a non-2xx response, closing the
// body after reading it.
func httpError(resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("ollama: http %d: %s", resp.StatusCode, bytes.TrimSpace(b))
}

// Suppress unused lint for the generic wireTool envelope — retained in case
// future Ollama API variants require it.
var _ = wireTool{}
