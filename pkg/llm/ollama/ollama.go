// Package ollama is the tinyagents adapter for a local or remote Ollama
// server. It talks to the /api/chat, /api/embed, and /api/tags endpoints
// directly via net/http with stdlib encoding/json — tinyagents does not
// depend on the upstream ollama module so nothing pulls in the server-side
// code paths or their call graph.
package ollama

import (
	"net/http"
)

const (
	// DefaultBaseURL is the usual local Ollama endpoint.
	DefaultBaseURL = "http://localhost:11434"
	providerName   = "ollama"
)

// Provider implements llm.Provider against an Ollama HTTP endpoint.
type Provider struct {
	baseURL string
	http    *http.Client
	name    string
}

// Option configures a Provider at construction time.
type Option func(*Provider)

// WithBaseURL overrides DefaultBaseURL. The value must be a full URL
// including scheme (e.g. "http://localhost:11434").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient provides a custom http.Client (timeouts, transports, …).
func WithHTTPClient(h *http.Client) Option { return func(p *Provider) { p.http = h } }

// WithName overrides Name(); useful when exposing an Ollama-compat server
// under a different brand in the llm.Registry.
func WithName(name string) Option { return func(p *Provider) { p.name = name } }

// New builds a Provider. An error is returned only for invalid options in
// future revisions; today the constructor is total but keeps the signature
// stable with other adapters.
func New(opts ...Option) (*Provider, error) {
	p := &Provider{
		baseURL: DefaultBaseURL,
		http:    http.DefaultClient,
		name:    providerName,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// Name returns the provider identifier used by llm.Registry.
func (p *Provider) Name() string { return p.name }
