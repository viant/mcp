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

// WithClientHandler with clientHandler
func WithClientHandler(handler client.Handler) Option {
	return func(c *Client) {
		c.clientHandler = handler
	}
}

func WithProtocolVersion(version string) Option {
	return func(c *Client) {
		c.protocolVersion = version
	}
}
