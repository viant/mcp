<!-- Automatically generated. Guided implementation documentation for MCP implementers. -->
# Implementer Guide

MCP implementers provide the application-specific functionality for the MCP protocol. Implementers must satisfy the `server.Handler` interface, handling protocol methods such as resource listing, reading, tool invocation, and more.

## Handler Interface

A handler is registered via the `server.NewHandler` factory:
```go
type NewHandler func(
    ctx context.Context,
    notifier transport.Notifier,
    logger logger.Logger,
    client client.Operations,
) server.Handler
```

Your implementer should embed `server.DefaultHandler` to leverage common functionality and implement the methods corresponding to the MCP schema you need. Key methods include:
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

// MyServer implements the MCP protocol methods.
// Embed DefaultHandler for common behaviour.
type MyHandler struct {
	*server.DefaultHandler
	// Add your custom fields here
}

// ListResources lists available resources.
func (i *MyHandler) ListResources(
	ctx context.Context,
	req *schema.ListResourcesRequest,
) (*schema.ListResourcesResult, *jsonrpc.Error) {
	// Implement resource listing
	return &schema.ListResourcesResult{Resources: nil}, nil
}

// Implements indicates which methods are supported.

func (i *MyHandler) Implements(method string) bool {
	switch method {
	case schema.MethodResourcesList:
		return true
	}
	return i.DefaultHandler.Implements(method) // delegate to DefaultHandler
}

// New returns a factory for MyServer.
func New() server.NewHandler {
	return func(
		ctx context.Context,
		notifier transport.Notifier,
		logger logger.Logger,
		client client.Operations,
) (server.Handler, error) {
		base := server.NewDefaultHandler(notifier, logger, client)
		return &MyHandler{DefaultHandler: base}, nil
	}
}

```

## Example: Comprehensive Custom Server Implementation
Use the `example/custom` package for a more advanced implementation with polling, notifications, and resource watching:

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
	newHandler := custom.New(config)
	srv, err := server.New(
		server.WithNewHandler(newHandler),
		server.WithImplementation(schema.Implementation{"custom", "1.0"}),
	)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
}
```