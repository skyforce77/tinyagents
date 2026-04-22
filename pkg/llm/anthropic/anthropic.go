// Package anthropic is the tinyagents adapter for the Anthropic Messages API.
// It wraps github.com/anthropics/anthropic-sdk-go.
// Use WithBaseURL to point the client at a custom endpoint (useful for testing
// with httptest servers) and WithName to rebrand the provider in the Registry.
package anthropic

import (
	"net/http"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const providerName = "anthropic"

// Provider implements llm.Provider on top of the Anthropic Messages API.
type Provider struct {
	client anthropicsdk.Client
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

// WithAPIKey sets the Anthropic API key.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL overrides the API root — essential for test servers.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.http = h } }

// WithName overrides Name() — useful when registering under a different brand.
func WithName(n string) Option { return func(c *config) { c.name = n } }

// New builds a Provider from the given options.
func New(opts ...Option) *Provider {
	cfg := config{name: providerName}
	for _, o := range opts {
		o(&cfg)
	}

	sdkOpts := []option.RequestOption{}
	if cfg.apiKey != "" {
		sdkOpts = append(sdkOpts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.baseURL != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(cfg.baseURL))
	}
	if cfg.http != nil {
		sdkOpts = append(sdkOpts, option.WithHTTPClient(cfg.http))
	}

	return &Provider{
		client: anthropicsdk.NewClient(sdkOpts...),
		name:   cfg.name,
	}
}

// Name returns the provider identifier used by llm.Registry.
func (p *Provider) Name() string { return p.name }
