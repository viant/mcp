package client

import (
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

// Option represents option
type Option func(c *Client)

// WithCapabilities set capabilites
func WithCapabilities(capabilities schema.ClientCapabilities) Option {
	return func(c *Client) {
		c.capabilities = capabilities
	}
}

// WithMetadata with meta
func WithMetadata(metadata map[string]any) Option {
	return func(c *Client) {
		c.meta = metadata
	}
}

// WithImplementer with implementer
func WithImplementer(impl client.Implementer) Option {
	return func(c *Client) {
		c.implementer = impl
	}
}
