package client

import (
	"context"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	"time"
)

type RequestOptions struct {
	RequestId   jsonrpc.RequestId
	StringToken string
}

func NewRequestOptions(options []RequestOption) *RequestOptions {
	requestOptions := &RequestOptions{}
	for _, option := range options {
		option(requestOptions)
	}
	return requestOptions
}

type RequestOption func(*RequestOptions)

func WithJsonRpcRequestId(requestId jsonrpc.RequestId) RequestOption {
	return func(options *RequestOptions) {
		options.RequestId = requestId
	}
}

func WithAuthToken(token string) RequestOption {
	return func(options *RequestOptions) {
		options.StringToken = token
	}
}

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
func WithClientHandler(handler pclient.Handler) Option {
	return func(c *Client) {
		c.clientHandler = handler
	}
}

func WithProtocolVersion(version string) Option {
	return func(c *Client) {
		c.protocolVersion = version
	}
}

// WithPingInterval enables a background pinger with the specified interval.
// Set to a positive duration (e.g., 30*time.Second) to keep sessions warm
// and detect/recover from transport issues. Zero or negative disables pinger.
func WithPingInterval(interval time.Duration) Option {
	return func(c *Client) {
		c.pingInterval = interval
	}
}

// WithReconnect sets reconnect function that can rebuild transport and perform re-initialization.
// It is used internally to automatically recover from transport-level errors like expired sessions.
// External callers typically do not need to set this option directly – it is configured by the
// mcp.NewClient helper that builds an MCP client from ClientOptions.
func WithReconnect(reconnect func(ctx context.Context) (transport.Transport, error)) Option {
	return func(c *Client) {
		c.reconnect = reconnect
	}
}
