package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/llm/anthropic"
)

// writeSSE writes a Server-Sent Event in the format the Anthropic SDK expects.
func writeSSE(w io.Writer, f http.Flusher, eventType string, data any) {
	b, _ := json.Marshal(data)
	_, _ = io.WriteString(w, "event: "+eventType+"\n")
	_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
	f.Flush()
}

func TestChat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		// Verify system prompt extraction: body should have a system field.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if sys, ok := body["system"]; !ok || sys == nil {
			t.Errorf("expected system field in request body, got %v", body["system"])
		}
		// Verify no system-role messages in messages array.
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				if msg, ok := m.(map[string]any); ok {
					if msg["role"] == "system" {
						t.Errorf("system role message found in messages array")
					}
				}
			}
		}

		resp := map[string]any{
			"id":    "msg_1",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-3-5-sonnet-20241022",
			"content": []map[string]any{
				{"type": "text", "text": "Hello!"},
			},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := anthropic.New(
		anthropic.WithBaseURL(srv.URL),
		anthropic.WithAPIKey("sk-test"),
	)

	out, err := p.Chat(context.Background(), llm.ChatRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are helpful."},
			{Role: llm.RoleUser, Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Content != "Hello!" {
		t.Fatalf("content = %q, want %q", out.Message.Content, "Hello!")
	}
	if out.FinishReason != "end_turn" {
		t.Fatalf("finish_reason = %q, want %q", out.FinishReason, "end_turn")
	}
	if out.Usage == nil {
		t.Fatal("usage is nil")
	}
	if out.Usage.PromptTokens != 10 || out.Usage.CompletionTokens != 5 || out.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestStreamText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)

		writeSSE(w, fl, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant",
				"model": "claude-3-5-sonnet-20241022", "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
			},
		})
		writeSSE(w, fl, "content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		writeSSE(w, fl, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hel"},
		})
		writeSSE(w, fl, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "lo"},
		})
		writeSSE(w, fl, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "!"},
		})
		writeSSE(w, fl, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": 0,
		})
		writeSSE(w, fl, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "end_turn", "stop_sequence": nil,
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 3},
		})
		writeSSE(w, fl, "message_stop", map[string]any{
			"type": "message_stop",
		})
	}))
	defer srv.Close()

	p := anthropic.New(anthropic.WithBaseURL(srv.URL), anthropic.WithAPIKey("sk-test"))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	var final llm.Chunk
	for c := range ch {
		text.WriteString(c.Delta)
		if c.FinishReason != "" {
			final = c
		}
	}

	if text.String() != "Hello!" {
		t.Fatalf("text = %q, want %q", text.String(), "Hello!")
	}
	if final.FinishReason != "end_turn" {
		t.Fatalf("finish_reason = %q, want %q", final.FinishReason, "end_turn")
	}
	if final.Usage == nil || final.Usage.TotalTokens != 13 {
		t.Fatalf("usage = %+v", final.Usage)
	}
}

func TestStreamToolCall(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)

		writeSSE(w, fl, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg_2", "type": "message", "role": "assistant",
				"model": "claude-3-5-sonnet-20241022", "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 20, "output_tokens": 0},
			},
		})
		// Tool use block starts at index 0.
		writeSSE(w, fl, "content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{
				"type": "tool_use", "id": "toolu_1",
				"name": "lookup", "input": map[string]any{},
			},
		})
		// First fragment of JSON arguments.
		writeSSE(w, fl, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"q":`},
		})
		// Second fragment.
		writeSSE(w, fl, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `"abc"}`},
		})
		writeSSE(w, fl, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": 0,
		})
		writeSSE(w, fl, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "tool_use", "stop_sequence": nil,
			},
			"usage": map[string]any{"input_tokens": 20, "output_tokens": 15},
		})
		writeSSE(w, fl, "message_stop", map[string]any{
			"type": "message_stop",
		})
	}))
	defer srv.Close()

	p := anthropic.New(anthropic.WithBaseURL(srv.URL), anthropic.WithAPIKey("sk-test"))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "look up abc"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var calls []llm.ToolCall
	var final llm.Chunk
	for c := range ch {
		if c.ToolCall != nil {
			calls = append(calls, *c.ToolCall)
		}
		if c.FinishReason != "" {
			final = c
		}
	}

	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].ID != "toolu_1" {
		t.Fatalf("tool id = %q, want %q", calls[0].ID, "toolu_1")
	}
	if calls[0].Name != "lookup" {
		t.Fatalf("tool name = %q, want %q", calls[0].Name, "lookup")
	}
	if string(calls[0].Arguments) != `{"q":"abc"}` {
		t.Fatalf("args = %q, want %q", string(calls[0].Arguments), `{"q":"abc"}`)
	}
	if final.FinishReason != "tool_use" {
		t.Fatalf("finish_reason = %q, want %q", final.FinishReason, "tool_use")
	}
}

func TestEmbed(t *testing.T) {
	t.Parallel()
	p := anthropic.New(anthropic.WithAPIKey("sk-test"))
	_, err := p.Embed(context.Background(), llm.EmbedRequest{
		Model: "some-model",
		Input: []string{"hello"},
	})
	if err == nil {
		t.Fatal("expected error from Embed, got nil")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %q, expected to contain %q", err.Error(), "not supported")
	}
}

func TestName(t *testing.T) {
	t.Parallel()
	p := anthropic.New()
	if p.Name() != "anthropic" {
		t.Fatalf("name = %q, want %q", p.Name(), "anthropic")
	}
	p2 := anthropic.New(anthropic.WithName("claude"))
	if p2.Name() != "claude" {
		t.Fatalf("name = %q, want %q", p2.Name(), "claude")
	}
}
