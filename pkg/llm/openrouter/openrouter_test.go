package openrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/llm/openai"
	"github.com/skyforce77/tinyagents/pkg/llm/openrouter"
)

func TestOpenRouterReusesOpenAIShape(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer or-key" {
			t.Errorf("auth = %q", auth)
		}
		resp := map[string]any{
			"id":      "1",
			"object":  "chat.completion",
			"created": 1,
			"model":   "openai/gpt-4o-mini",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "hi"},
				"finish_reason": "stop",
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// WithBaseURL after the defaults overrides the helper's DefaultBaseURL,
	// pointing the wrapper at our httptest server without changing the
	// client identity.
	p := openrouter.New("or-key", openai.WithBaseURL(srv.URL))
	if p.Name() != "openrouter" {
		t.Fatalf("name = %q", p.Name())
	}
	out, err := p.Chat(context.Background(), llm.ChatRequest{
		Model:    "openai/gpt-4o-mini",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Content != "hi" {
		t.Fatalf("content = %q", out.Message.Content)
	}
}
