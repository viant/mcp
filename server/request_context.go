package server

import (
	"context"

	"github.com/viant/mcp-protocol/schema"
)

type protocolRequestContextKey struct{}

// ProtocolRequest describes the immutable July metadata attached to one MCP
// request. It must not be retained or reused for a later request.
type ProtocolRequest struct {
	Version      string
	Capabilities schema.ClientCapabilities
	ClientInfo   *schema.Implementation
	LogLevel     *schema.LoggingLevel
}

// ProtocolRequestFromContext returns the current request's July metadata.
func ProtocolRequestFromContext(ctx context.Context) (*ProtocolRequest, bool) {
	value, ok := ctx.Value(protocolRequestContextKey{}).(*ProtocolRequest)
	return value, ok && value != nil
}

func withProtocolRequest(ctx context.Context, request *ProtocolRequest) context.Context {
	return context.WithValue(ctx, protocolRequestContextKey{}, request)
}
