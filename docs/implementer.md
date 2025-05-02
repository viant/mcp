<!-- Automatically generated. Guided implementation documentation for MCP implementers. -->
# Implementer Guide

MCP implementers provide the application-specific functionality for the MCP protocol. Implementers must satisfy the `server.Implementer` interface, handling protocol methods such as resource listing, reading, tool invocation, and more.

## Implementer Interface

An implementer is registered via the `server.NewImplementer` factory:
```go
type NewImplementer func(
    ctx context.Context,
    notifier transport.Notifier,
    logger logger.Logger,
    client client.Operations,
) server.Implementer
```

Your implementer should embed `implementer.Base` to leverage common functionality and implement the methods corresponding to the MCP schema you need. Key methods include:
- `ListResources` (`resources/list`)
- `ReadResource` (`resources/read`)
- `ListTools` (`tools/list`)
- `CallTool` (`tools/call`)
- `Implements` (indicate which methods your implementer supports)

## Example: Custom Implementer Skeleton
```go
package myimplementer

import (
    "context"
    "github.com/viant/jsonrpc"
    "github.com/viant/jsonrpc/transport"
    "github.com/viant/mcp/implementer"
    "github.com/viant/mcp/logger"
    "github.com/viant/mcp/protocol/server"
    "github.com/viant/mcp/schema"
)

// MyImplementer implements the MCP protocol methods.
type MyImplementer struct {
    *implementer.Base
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
    return false
}

// New returns a factory for MyImplementer.
func New() server.NewImplementer {
    return func(
        ctx context.Context,
        notifier transport.Notifier,
        logger logger.Logger,
        client client.Operations,
    ) server.Implementer {
        base := implementer.New(notifier, logger, client)
        return &MyImplementer{Base: base}
    }
}
```

## Example: Filesystem Implementer
The `example/fs` package provides a complete filesystem-based implementer using Go embed. To use:
```go
config := &fs.Config{
    BaseURL: "embed:///resources",
    Options: []storage.Option{resourceFS},
}
newImplementer := fs.New(config)
srv, _ := server.New(
    server.WithNewImplementer(newImplementer),
    server.WithImplementation(schema.Implementation{"MyMCP", "1.0"}),
    server.WithCapabilities(schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}}),
)
httpSrv := srv.HTTP(context.Background(), ":4981")
log.Fatal(httpSrv.ListenAndServe())
```