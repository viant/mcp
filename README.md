# MCP (Model Context Protocol) for Go

MCP is a Go implementation of the Model Context Protocol — a standardized way for applications to communicate with AI models. It allows developers to seamlessly bridge applications and AI models using a lightweight, JSON-RPC–based protocol.

[Official Model Context Protocol Specification](https://modelcontextprotocol.io/introduction)


## Overview

MCP (Model Context Protocol) is designed to provide a standardized communication layer between applications and AI models. The protocol simplifies the integration of AI capabilities into applications by offering a consistent interface for resource access, prompt management, model interaction, and tool invocation.

Key features:
- JSON-RPC 2.0–based communication
- Support for multiple transport protocols (HTTP/SSE, stdio)
- Server-side features:
  - Resource management
  - Model prompting and completion
  - Tool invocation
  - Subscriptions for resource updates
  - Logging
  - Progress reporting
  - Request cancellation
- Client-side features:
  - Roots
  - Sampling


## Architecture

For detailed guides on custom implementers and authentication, see [docs/implementer.md](docs/implementer.md) and [docs/authentication.md](docs/authentication.md).

MCP is built around the following components:

1. **Protocol**: Defines the communication format and methods
2. **Server**: Handles incoming requests and dispatches to implementers
3. **Client**: Makes requests to MCP-compatible servers
4. **Implementer**: Provides the actual functionality behind each protocol method

## Getting Started

### Installation

```bash
go get github.com/viant/mcp
```

### Creating a Server

To create an MCP server, you need to provide an implementer that handles the protocol methods:

```go
package main

import (
    "context"
    "github.com/viant/mcp/example/fs"
    "github.com/viant/mcp/protocol/server"
    "github.com/viant/mcp/schema"
	"embed"
    "log"
)



//go:embed data/*
var embedFs embed.FS

func main() {
    // Create a configuration for the filesystem implementer (for this example, we use the Go embed filesystem)
    config := &fs.Config{
        BaseURL: "embed://data",
    }
    
    // Create a new implementer
    newImplementer := fs.New(config)
    
    // Configure server options
    options := []server.Option{
        server.WithNewImplementer(newImplementer),
        server.WithImplementation(schema.Implementation{"My MCP Server", "1.0"}),
        server.WithCapabilities(schema.ServerCapabilities{
            Resources: &schema.ServerCapabilitiesResources{},
        }),
    }
    
    // Create and start the server
    srv, err := server.New(options...)
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }
    
    // Start an HTTP server
    ctx := context.Background()
    endpoint := srv.HTTP(ctx, ":4981")
    log.Fatal(endpoint.ListenAndServe())
}
```


### Creating a Client

To connect to an MCP server:

```go
package main

import (
    "context"
    "fmt"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/protocol/client"
    "github.com/viant/mcp/schema"
    "log"
)

func main() {
    ctx := context.Background()
    
    // Create a transport (HTTP/SSE in this example)
    transport, err := sse.New(ctx, "http://localhost:4981/sse")
    if err != nil {
        log.Fatalf("Failed to create transport: %v", err)
    }
    
    // Create the client
    mcpClient := client.New(
        "MyClient", 
        "1.0", 
        transport, 
        client.WithCapabilities(schema.ClientCapabilities{}),
    )
    
    // Initialize the client
    result, err := mcpClient.Initialize(ctx)
    if err != nil {
        log.Fatalf("Failed to initialize: %v", err)
    }
    
    fmt.Printf("Connected to %s %s\n", result.ServerInfo.Name, result.ServerInfo.Version)
    
    // List resources
    resources, err := mcpClient.ListResources(ctx, nil)
    if err != nil {
        log.Fatalf("Failed to list resources: %v", err)
    }
    
    for _, resource := range resources.Resources {
        fmt.Printf("Resource: %s (%s)\n", resource.Name, resource.Uri)
    }
}
```

### Further Reading

- **Implementer Guide**: [docs/implementer.md](docs/implementer.md)
- **Authentication Guide**: [docs/authentication.md](docs/authentication.md)

## Protocol Methods

MCP supports the following Server Side methods:

- `initialize` - Initialize the connection
- `ping` - Check server status
- `resources/list` - List available resources
- `resources/read` - Read resource contents
- `resources/templates/list` - List resource templates
- `resources/subscribe` - Subscribe to resource updates
- `resources/unsubscribe` - Unsubscribe from resource updates
- `prompts/list` - List available prompts
- `prompts/get` - Get prompt details
- `tools/list` - List available tools
- `tools/call` - Call a specific tool
- `complete` - Get completions from the model
- `logging/setLevel` - Set logging level

MCP supports the following Client Side methods:

- `roots/list` - List available roots
- `sampling/createMessage` - A standardized way for servers to request LLM sampling (“completions” or “generations”) from language models via clients.


can 
## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [Apache License 2.0](LICENSE).

## Credits

Author: Adrian Witas

This project is maintained by [Viant](https://github.com/viant).
