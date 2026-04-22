// Package mistral is a thin convenience wrapper over the openai adapter.
// Mistral exposes an OpenAI-compatible Chat Completions endpoint, so reusing
// the openai client keeps the adapter count at one for the whole OpenAI-compat
// family (OpenRouter, Mistral, DeepSeek, …).
package mistral

import "github.com/skyforce77/tinyagents/pkg/llm/openai"

// DefaultBaseURL is Mistral's OpenAI-compatible API root.
const DefaultBaseURL = "https://api.mistral.ai/v1"

// New returns an openai.Provider pre-configured for Mistral. Extra
// openai.Option values can still be passed (e.g. WithHTTPClient); they
// are applied after the defaults so callers can override anything the
// helper set.
func New(apiKey string, opts ...openai.Option) *openai.Provider {
	all := []openai.Option{
		openai.WithBaseURL(DefaultBaseURL),
		openai.WithAPIKey(apiKey),
		openai.WithName("mistral"),
	}
	all = append(all, opts...)
	return openai.New(all...)
}
