package example

import (
	"context"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
)

// MyImplementer implements the MCP protocol methods.
// Embed DefaultImplementer for common behavior.
type MyImplementer struct {
	*server.DefaultImplementer
	// Add your custom fields here
}

// ListResources lists available resources.
func (i *MyImplementer) ListResources(
	ctx context.Context,
	req *schema.ListResourcesRequest,
) (*schema.ListResourcesResult, *jsonrpc.Error) {
	// Implement resource listing
	return &schema.ListResourcesResult{Resources: nil}, nil
}

// Implements indicates which methods are supported.
func (i *MyImplementer) Implements(method string) bool {
	switch method {
	case schema.MethodResourcesList:
		return true
	}
	return i.DefaultImplementer.Implements(method) //delegate to DefaultImplementer
}

// New returns a factory for MyImplementer.
func New() server.NewImplementer {
	return func(
		ctx context.Context,
		notifier transport.Notifier,
		logger logger.Logger,
		client client.Operations,
	) server.Implementer {
		base := server.NewDefaultImplementer(notifier, logger, client)
		return &MyImplementer{DefaultImplementer: base}
	}
}
