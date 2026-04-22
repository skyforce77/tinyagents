// Package ollama is the tinyagents adapter for a local Ollama server.
// It implements llm.Provider on top of the /api/chat, /api/embed, and
// /api/tags endpoints. Ollama requires no authentication, so the
// constructor only takes a base URL (default http://localhost:11434)
// and an optional HTTP client.
package ollama

import (
	"net/http"
	"time"
)

const (
	// DefaultBaseURL is the usual local Ollama endpoint.
	DefaultBaseURL = "http://localhost:11434"
	providerName   = "ollama"
)

// Provider implements llm.Provider.
type Provider struct {
	baseURL string
	http    *http.Client
}

// Option configures a Provider at construction time.
type Option func(*Provider)

// WithBaseURL overrides DefaultBaseURL.
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.http = c } }

// New builds a Provider with the given options.
func New(opts ...Option) *Provider {
	p := &Provider{
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name returns the provider identifier used by llm.Registry lookups.
func (p *Provider) Name() string { return providerName }
