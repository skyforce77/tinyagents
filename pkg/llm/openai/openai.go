// Package openai is the tinyagents adapter for the OpenAI Chat Completions
// API. It wraps github.com/sashabaranov/go-openai, which also covers the
// wire-compatible family (OpenRouter, Mistral openai-mode, DeepSeek, …).
// Use WithBaseURL to point the client at one of those endpoints and
// WithName to rebrand the provider in the llm.Registry.
package openai

import (
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"
)

const providerName = "openai"

// Provider implements llm.Provider on top of go-openai.
type Provider struct {
	client *goopenai.Client
	name   string
}

// Option configures a Provider at construction time.
type Option func(*config)

type config struct {
	apiKey  string
	baseURL string
	http    *http.Client
	name    string
}

// WithAPIKey sets the bearer token.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL overrides the API root — essential for OpenRouter / Mistral /
// DeepSeek, which mimic OpenAI's wire format at a different hostname.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.http = h } }

// WithName overrides Name() — useful when registering the same wire format
// under a different brand in the llm.Registry (e.g. "openrouter").
func WithName(n string) Option { return func(c *config) { c.name = n } }

// New builds a Provider. apiKey is usually required; an empty key keeps
// calls unauthenticated (useful for local openai-compat servers).
func New(opts ...Option) *Provider {
	cfg := config{name: providerName}
	for _, o := range opts {
		o(&cfg)
	}
	gc := goopenai.DefaultConfig(cfg.apiKey)
	if cfg.baseURL != "" {
		gc.BaseURL = cfg.baseURL
	}
	if cfg.http != nil {
		gc.HTTPClient = cfg.http
	}
	return &Provider{
		client: goopenai.NewClientWithConfig(gc),
		name:   cfg.name,
	}
}

// Name returns the provider identifier used by llm.Registry.
func (p *Provider) Name() string { return p.name }
