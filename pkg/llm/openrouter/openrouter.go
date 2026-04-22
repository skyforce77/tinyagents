// Package openrouter is a thin convenience wrapper over the openai adapter.
// OpenRouter mirrors OpenAI's Chat Completions wire format, so reusing the
// openai client keeps the adapter count at one for the whole OpenAI-compat
// family (OpenRouter, Mistral openai-mode, DeepSeek, …).
package openrouter

import "github.com/skyforce77/tinyagents/pkg/llm/openai"

// DefaultBaseURL is OpenRouter's API root.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// New returns an openai.Provider pre-configured for OpenRouter. Extra
// openai.Option values can still be passed (e.g. WithHTTPClient); they
// are applied after the defaults so callers can override anything the
// helper set.
func New(apiKey string, opts ...openai.Option) *openai.Provider {
	all := []openai.Option{
		openai.WithBaseURL(DefaultBaseURL),
		openai.WithAPIKey(apiKey),
		openai.WithName("openrouter"),
	}
	all = append(all, opts...)
	return openai.New(all...)
}
