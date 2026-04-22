// Package ollama is the tinyagents adapter for a local Ollama server.
// It wraps github.com/ollama/ollama/api so we do not maintain our own
// HTTP plumbing — the official client already handles chat, streaming
// (via callback), embeddings, and the /api/tags catalog.
package ollama

import (
	"net/http"
	"net/url"

	oapi "github.com/ollama/ollama/api"
)

const (
	// DefaultBaseURL is the usual local Ollama endpoint.
	DefaultBaseURL = "http://localhost:11434"
	providerName   = "ollama"
)

// Provider implements llm.Provider by delegating to the upstream
// github.com/ollama/ollama/api.Client.
type Provider struct {
	client *oapi.Client
	name   string
}

// Option configures a Provider at construction time.
type Option func(*config)

type config struct {
	baseURL string
	http    *http.Client
	name    string
}

// WithBaseURL overrides DefaultBaseURL.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithHTTPClient provides a custom http.Client (timeouts, transports, …).
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.http = h } }

// WithName overrides Name(); useful when exposing an Ollama-compat server
// under a different brand in the llm.Registry.
func WithName(name string) Option { return func(c *config) { c.name = name } }

// New builds a Provider.
func New(opts ...Option) (*Provider, error) {
	cfg := config{baseURL: DefaultBaseURL, name: providerName, http: http.DefaultClient}
	for _, o := range opts {
		o(&cfg)
	}
	u, err := url.Parse(cfg.baseURL)
	if err != nil {
		return nil, err
	}
	return &Provider{
		client: oapi.NewClient(u, cfg.http),
		name:   cfg.name,
	}, nil
}

// Name returns the provider identifier used by llm.Registry.
func (p *Provider) Name() string { return p.name }
