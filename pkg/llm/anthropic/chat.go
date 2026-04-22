package anthropic

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

const defaultMaxTokens = 1024

// toolAcc accumulates streaming tool call fragments for a single content block.
type toolAcc struct {
	id   string
	name string
	args strings.Builder
}

// Chat issues a non-streaming completion.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	params, err := buildParams(req)
	if err != nil {
		return llm.ChatResponse{}, err
	}

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return llm.ChatResponse{}, err
	}

	return llm.ChatResponse{
		Message:      contentToMessage(msg.Content),
		FinishReason: string(msg.StopReason),
		Usage:        toUsage(msg.Usage),
	}, nil
}

// Stream issues a streaming completion. Text deltas are emitted as they arrive.
// Tool-call argument fragments (input_json_delta) are accumulated per content
// block index and emitted as a single Chunk{ToolCall} at message_stop. The
// terminal chunk carries FinishReason and Usage.
func (p *Provider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}

	stream := p.client.Messages.NewStreaming(ctx, params)
	if stream == nil {
		return nil, nil
	}

	ch := make(chan llm.Chunk, 16)
	go func() {
		defer close(ch)
		defer stream.Close() //nolint:errcheck

		// Track tool accumulation per content block index.
		toolBuf := map[int64]*toolAcc{}

		var finishReason string
		var usage *llm.Usage

		for stream.Next() {
			evt := stream.Current()
			switch evt.Type {
			case "content_block_start":
				cb := evt.AsContentBlockStart()
				if cb.ContentBlock.Type == "tool_use" {
					toolBuf[cb.Index] = &toolAcc{
						id:   cb.ContentBlock.ID,
						name: cb.ContentBlock.Name,
					}
				}

			case "content_block_delta":
				cbd := evt.AsContentBlockDelta()
				switch cbd.Delta.Type {
				case "text_delta":
					td := cbd.Delta.AsTextDelta()
					if td.Text != "" {
						select {
						case ch <- llm.Chunk{Delta: td.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "input_json_delta":
					ij := cbd.Delta.AsInputJSONDelta()
					if acc, ok := toolBuf[cbd.Index]; ok {
						acc.args.WriteString(ij.PartialJSON)
					}
				}

			case "message_delta":
				md := evt.AsMessageDelta()
				finishReason = string(md.Delta.StopReason)
				usage = &llm.Usage{
					PromptTokens:     int(md.Usage.InputTokens),
					CompletionTokens: int(md.Usage.OutputTokens),
					TotalTokens:      int(md.Usage.InputTokens + md.Usage.OutputTokens),
				}

			case "message_stop":
				// Emit accumulated tool calls in index order.
				keys := sortedToolKeys(toolBuf)
				for _, k := range keys {
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
				// Emit terminal chunk.
				select {
				case ch <- llm.Chunk{FinishReason: finishReason, Usage: usage}:
				case <-ctx.Done():
				}
				return
			}
		}

		if err := stream.Err(); err != nil {
			// Stream ended with error — emit terminal chunk so callers don't hang.
			select {
			case ch <- llm.Chunk{FinishReason: "error"}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// buildParams converts a llm.ChatRequest into the SDK's MessageNewParams.
// System messages are extracted and concatenated into the top-level system field
// because Anthropic does not support a "system" role in the messages array.
func buildParams(req llm.ChatRequest) (anthropicsdk.MessageNewParams, error) {
	maxTokens := int64(defaultMaxTokens)
	if req.MaxTokens != nil {
		maxTokens = int64(*req.MaxTokens)
	}

	// Extract system prompt from messages.
	var systemParts []string
	var msgs []anthropicsdk.MessageParam
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
		case llm.RoleUser:
			blocks := []anthropicsdk.ContentBlockParamUnion{
				anthropicsdk.NewTextBlock(m.Content),
			}
			msgs = append(msgs, anthropicsdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
			blocks := assistantBlocks(m)
			msgs = append(msgs, anthropicsdk.NewAssistantMessage(blocks...))
		case llm.RoleTool:
			// Tool results go as user messages with tool_result blocks.
			block := anthropicsdk.NewToolResultBlock(m.ToolCallID, m.Content, false)
			msgs = append(msgs, anthropicsdk.NewUserMessage(block))
		}
	}

	params := anthropicsdk.MessageNewParams{
		Model:         anthropicsdk.Model(req.Model),
		MaxTokens:     maxTokens,
		Messages:      msgs,
		StopSequences: req.Stop,
	}

	if len(systemParts) > 0 {
		sysText := strings.Join(systemParts, "\n")
		params.System = []anthropicsdk.TextBlockParam{
			{Text: sysText},
		}
	}

	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}

	// Build tools.
	if len(req.Tools) > 0 {
		tools := make([]anthropicsdk.ToolUnionParam, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := buildInputSchema(t.Schema)
			tp := anthropicsdk.ToolUnionParamOfTool(schema, t.Name)
			if t.Description != "" && tp.OfTool != nil {
				tp.OfTool.Description = param.NewOpt(t.Description)
			}
			tools = append(tools, tp)
		}
		params.Tools = tools
	}

	return params, nil
}

// assistantBlocks converts an assistant llm.Message into content blocks.
func assistantBlocks(m llm.Message) []anthropicsdk.ContentBlockParamUnion {
	var blocks []anthropicsdk.ContentBlockParamUnion
	if m.Content != "" {
		blocks = append(blocks, anthropicsdk.NewTextBlock(m.Content))
	}
	for _, tc := range m.ToolCalls {
		var input any
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &input)
		}
		blocks = append(blocks, anthropicsdk.NewToolUseBlock(tc.ID, input, tc.Name))
	}
	return blocks
}

// buildInputSchema converts a raw JSON schema into ToolInputSchemaParam.
// The schema may contain extra fields beyond what the struct knows about, so
// we deserialise everything and distribute into the known fields and ExtraFields.
func buildInputSchema(raw json.RawMessage) anthropicsdk.ToolInputSchemaParam {
	if len(raw) == 0 {
		return anthropicsdk.ToolInputSchemaParam{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return anthropicsdk.ToolInputSchemaParam{}
	}
	schema := anthropicsdk.ToolInputSchemaParam{
		ExtraFields: make(map[string]any),
	}
	for k, v := range m {
		switch k {
		case "properties":
			schema.Properties = v
		case "required":
			if arr, ok := v.([]any); ok {
				for _, s := range arr {
					if str, ok := s.(string); ok {
						schema.Required = append(schema.Required, str)
					}
				}
			}
		case "type":
			// "type" is handled by the constant.Object default; skip it.
		default:
			schema.ExtraFields[k] = v
		}
	}
	return schema
}

// contentToMessage converts a []ContentBlockUnion from a non-streaming response
// into an llm.Message (always assistant role).
func contentToMessage(blocks []anthropicsdk.ContentBlockUnion) llm.Message {
	m := llm.Message{Role: llm.RoleAssistant}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			tb := b.AsText()
			m.Content += tb.Text
		case "tool_use":
			tu := b.AsToolUse()
			m.ToolCalls = append(m.ToolCalls, llm.ToolCall{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: tu.Input,
			})
		}
	}
	return m
}

// toUsage converts the SDK's Usage struct to llm.Usage.
func toUsage(u anthropicsdk.Usage) *llm.Usage {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     int(u.InputTokens),
		CompletionTokens: int(u.OutputTokens),
		TotalTokens:      int(u.InputTokens + u.OutputTokens),
	}
}

func sortedToolKeys(m map[int64]*toolAcc) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
