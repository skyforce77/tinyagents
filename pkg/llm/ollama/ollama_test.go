package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/llm/ollama"
)

func TestChat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "llama3.2" {
			t.Errorf("model = %v, want llama3.2", req["model"])
		}
		resp := map[string]any{
			"model":             "llama3.2",
			"message":           map[string]any{"role": "assistant", "content": "howdy"},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 10,
			"eval_count":        5,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := ollama.New(ollama.WithBaseURL(srv.URL))
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Model: "llama3.2",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "howdy" {
		t.Fatalf("content = %q, want howdy", resp.Message.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frames := []string{
			`{"model":"llama","message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"model":"llama","message":{"role":"assistant","content":"lo"},"done":false}`,
			`{"model":"llama","message":{"role":"assistant","content":"!"},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}`,
		}
		fl := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n")
			fl.Flush()
		}
	}))
	defer srv.Close()

	p := ollama.New(ollama.WithBaseURL(srv.URL))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{
		Model: "llama",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "say hi"},
		},
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
		t.Fatalf("text = %q, want Hello!", text.String())
	}
	if final.FinishReason != "stop" {
		t.Fatalf("final finish = %q", final.FinishReason)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", final.Usage)
	}
}

func TestStreamToolCall(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-frame tool call response.
		frame := `{"model":"llama","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"lookup","arguments":{"q":"abc"}}}]},"done":true,"done_reason":"stop"}`
		_, _ = io.WriteString(w, frame+"\n")
	}))
	defer srv.Close()

	p := ollama.New(ollama.WithBaseURL(srv.URL))
	ch, err := p.Stream(context.Background(), llm.ChatRequest{Model: "llama"})
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
}

func TestEmbed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		resp := map[string]any{
			"model":             "nomic-embed-text",
			"embeddings":        [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
			"prompt_eval_count": 4,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := ollama.New(ollama.WithBaseURL(srv.URL))
	resp, err := p.Embed(context.Background(), llm.EmbedRequest{
		Model: "nomic-embed-text",
		Input: []string{"alpha", "beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(resp.Vectors))
	}
	if resp.Vectors[0][0] != 0.1 {
		t.Fatalf("vector[0][0] = %v", resp.Vectors[0][0])
	}
}

func TestModels(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "llama3.2:latest"},
				{"name": "mistral:7b"},
			},
		})
	}))
	defer srv.Close()

	p := ollama.New(ollama.WithBaseURL(srv.URL))
	ms, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d models, want 2", len(ms))
	}
	if ms[0].ID != "llama3.2:latest" {
		t.Fatalf("id[0] = %q", ms[0].ID)
	}
}

func TestChatHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	}))
	defer srv.Close()
	p := ollama.New(ollama.WithBaseURL(srv.URL))
	_, err := p.Chat(context.Background(), llm.ChatRequest{Model: "ghost"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error does not mention status: %v", err)
	}
}
