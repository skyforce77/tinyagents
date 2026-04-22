package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/llm/openai"
)

func TestChat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-test" {
			t.Errorf("auth = %q", auth)
		}
		resp := map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-4o-mini",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "howdy"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := openai.New(
		openai.WithBaseURL(srv.URL),
		openai.WithAPIKey("sk-test"),
	)
	out, err := p.Chat(context.Background(), llm.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Content != "howdy" {
		t.Fatalf("content = %q", out.Message.Content)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		write := func(s string) {
			_, _ = io.WriteString(w, s)
			fl.Flush()
		}
		write(`data: {"id":"1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":""}]}` + "\n\n")
		write(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":""}]}` + "\n\n")
		write(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := openai.New(openai.WithBaseURL(srv.URL), openai.WithAPIKey("sk-test"))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{Model: "gpt-4o"})
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
		t.Fatalf("text = %q", text.String())
	}
	if final.FinishReason != "stop" {
		t.Fatalf("finish = %q", final.FinishReason)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", final.Usage)
	}
}

func TestStreamToolCallsAcrossFrames(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		write := func(s string) {
			_, _ = io.WriteString(w, s)
			fl.Flush()
		}
		write(`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}` + "\n\n")
		write(`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"abc\"}"}}]}}]}` + "\n\n")
		write(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := openai.New(openai.WithBaseURL(srv.URL))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	var calls []llm.ToolCall
	for c := range ch {
		if c.ToolCall != nil {
			calls = append(calls, *c.ToolCall)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].Name != "lookup" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if string(calls[0].Arguments) != `{"q":"abc"}` {
		t.Fatalf("args = %q", string(calls[0].Arguments))
	}
}

func TestEmbed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.4, 0.5}, "object": "embedding"},
				{"index": 0, "embedding": []float32{0.1, 0.2}, "object": "embedding"},
			},
			"model": "text-embedding-3-small",
			"usage": map[string]any{"prompt_tokens": 2, "total_tokens": 2},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := openai.New(openai.WithBaseURL(srv.URL))
	out, err := p.Embed(context.Background(), llm.EmbedRequest{
		Model: "text-embedding-3-small",
		Input: []string{"alpha", "beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Vectors) != 2 {
		t.Fatalf("vectors = %d", len(out.Vectors))
	}
	if out.Vectors[0][0] != 0.1 || out.Vectors[1][0] != 0.4 {
		t.Fatalf("reorder failed: %v", out.Vectors)
	}
}

func TestModels(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-4o", "object": "model"},
				{"id": "gpt-4o-mini", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	p := openai.New(openai.WithBaseURL(srv.URL))
	ms, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "gpt-4o" {
		t.Fatalf("models = %+v", ms)
	}
}

func TestNameOverride(t *testing.T) {
	t.Parallel()
	p := openai.New(openai.WithName("openrouter"))
	if p.Name() != "openrouter" {
		t.Fatalf("name = %q", p.Name())
	}
}
