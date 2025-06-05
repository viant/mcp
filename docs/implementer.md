<!-- Automatically generated. Guided implementation documentation for MCP implementers. -->
# Implementer Guide

MCP implementers provide the application-specific functionality for the MCP protocol. Implementers must satisfy the `server.Implementer` interface, handling protocol methods such as resource listing, reading, tool invocation, and more.

## Implementer Interface

An implementer is registered via the `server.NewServer` factory:
```go
type NewServer func(
    ctx context.Context,
    notifier transport.Notifier,
    logger logger.Logger,
    client client.Operations,
) server.Implementer
```

Your implementer should embed `server.DefaultImplementer` to leverage common functionality and implement the methods corresponding to the MCP schema you need. Key methods include:
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
func New() server.NewServer {
	return func(
		ctx context.Context,
		notifier transport.Notifier,
		logger logger.Logger,
		client client.Operations,
	) (server.Implementer, error) {
		base := server.NewDefaultImplementer(notifier, logger, client)
		return &MyImplementer{DefaultImplementer: base}, nil
	}
}

```

## Example: Comprehensive Custom Implementer
Use the `example/custom` package for a more advanced implementer with polling, notifications, and resource watching:
```go
package main

import (
    "context"
    "embed"
    "log"

    "github.com/viant/afs/storage"
    "github.com/viant/mcp/example/custom"
    "github.com/viant/mcp/server"
    "github.com/viant/mcp-protocol/schema"
)

//go:embed data/*
var embedFS embed.FS

func main() {
    config := &custom.Config{
        BaseURL: "embed://data",
        Options: []storage.Option{embedFS},
    }
    NewServer := custom.New(config)
    srv, err := server.New(
        server.WithNewServer(NewServer),
        server.WithImplementation(schema.Implementation{"custom", "1.0"}),
    )
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }
    log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
}
```