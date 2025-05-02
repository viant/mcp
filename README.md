# MCP (Model Context Protocol) for golang

MCP is a Go implementation of the Model Context Protocol — a standardized way for applications to communicate with AI models. It allows developers to seamlessly bridge applications and AI models using a lightweight, JSON-RPC–based protocol.

[Official Model Context Protocol Specification](https://modelcontextprotocol.io/introduction)


## Overview

MCP (Model Context Protocol) is designed to provide a standardized communication layer between applications and AI models. The protocol simplifies the integration of AI capabilities into applications by offering a consistent interface for resource access, prompt management, model interaction, and tool invocation.

Key features:
- JSON-RPC 2.0 based communication
- Support for multiple transport protocols (HTTP/SSE, stdio)
- Server Side Features
  - Resource management capabilities
  - Model prompting and completion
  - Tool invocation
  - Subscriptions for resource updates
  - Logging
  - Progress reporting
  - Request cancellation
- Client Side Features
  - Roots
  - Sampling


## Architecture

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
    // Create a configuration for the filesystem implementer (for this exmple we use go embed file system)
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

For servers enforcing authentication, configure the SSE transport with an HTTP client that injects the Bearer token:

```go
// authTransport adds the Authorization header to outgoing requests.
// Customize the HTTP client's RoundTripper.
type authTransport struct {
   base  http.RoundTripper
   token string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
   req.Header.Set("Authorization", "Bearer "+t.token)
   return t.base.RoundTrip(req)
}

httpClient := &http.Client{Transport: &authTransport{base: http.DefaultTransport, token: "<ACCESS_TOKEN>"}}
transport, err := sse.New(ctx, "http://localhost:4981/sse", sse.WithClient(httpClient))
if err != nil {
   log.Fatalf("Failed to create transport: %v", err)
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

## Creating Your Own Implementer

Implementers provide the actual functionality behind the MCP protocol. Here's a simple example of creating a custom implementer:

```go
package myimplementer

import (
	"context"
	"encoding/base64"
	"github.com/viant/afs"
	_ "github.com/viant/afs/embed"
	"mime"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/implementer"
	"github.com/viant/mcp/logger"
	"github.com/viant/gosh"
   "github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/protocol/server"
	"github.com/viant/mcp/schema"
)

type MyImplementer struct {
	*implementer.Base
	fs afs.Service
	terminal *gosh.Service
	// Add your custom fields here
}

func (i *MyImplementer) ListResources(ctx context.Context, request *schema.ListResourcesRequest) (*schema.ListResourcesResult, *jsonrpc.Error) {
	objects, err := i.fs.List(ctx, i.config.BaseURL, i.config.Options...)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	var resources []schema.Resource
	for _, object := range objects {
		if object.IsDir() {
			continue
		}
		name := object.Name()
		ext := filepath.Ext(name)
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		resource := &schema.Resource{
			Name:     name,
			MimeType: &mimeType,
			Uri:      object.URL(),
		}
		resources = append(resources, *resource)
	}
	return &schema.ListResourcesResult{Resources: resources}, nil
}

func (i *MyImplementer) ReadResource(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
	object, err := i.fs.Object(ctx, request.Params.Uri)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	data, err := i.fs.DownloadWithURL(ctx, request.Params.Uri, i.config.Options...)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}

	name := object.Name()
	ext := filepath.Ext(name)
	mimeType := mime.TypeByExtension(ext)

	var text string
	var blob string
	if isBinary(data) {
		blob = base64.StdEncoding.EncodeToString(data)
	} else {
		text = string(data)
	}

	result := schema.ReadResourceResult{}
	content := schema.ReadResourceResultContentsElem{
		MimeType: &mimeType,
		Uri:      object.URL(),
		Blob:     blob,
		Text:     text,
	}
	result.Contents = append(result.Contents, content)
	return &result, nil
}



func (i *MyImplementer) ListTools(ctx context.Context, request *schema.ListToolsRequest) (*schema.ListToolsResult, *jsonrpc.Error) {
  // Create a tool schema for the terminal command
  var terminalSchema schema.ToolInputSchema
  err := terminalSchema.Load(&TerminalCommand{})
  if err != nil {
    return nil, jsonrpc.NewInternalError(fmt.Sprintf("failed to create schema: %v", err), nil)
  }

  // Create the terminal tool
  tools := []schema.Tool{
    {
      Name:        "terminal",
      Description: func() *string { s := "Run terminal commands"; return &s }(),
      InputSchema: terminalSchema,
    },
  }

  return &schema.ListToolsResult{
    Tools: tools,
  }, nil
}

type TerminalCommand struct {
  Commands []string          `json:"commands"`
  Evn      map[string]string `json:"evn"`
}

func (i *MyImplementer) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
  if request.Params.Name != "terminal" {
    return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("tool %v not found", request.Params.Name), nil)
  }

  var command TerminalCommand
  data, err := json.Marshal(request.Params.Arguments)
  if err != nil {
    return nil, jsonrpc.NewInternalError(fmt.Sprintf("failed to marshal arguments: %v", err), nil)
  }

  if err := json.Unmarshal(data, &command); err != nil {
    return nil, jsonrpc.NewInternalError(fmt.Sprintf("invalid arguments: %v", err), nil)
  }

  // Convert commands to a single string command
  cmdString := ""
  if len(command.Commands) > 0 {
    cmdString = command.Commands[0]
    for i := 1; i < len(command.Commands); i++ {
      cmdString += " && " + command.Commands[i]
    }
  }

  // Run the command
  output, _, err := i.term.Run(ctx, cmdString)
  if err != nil {
    isError := true
    return &schema.CallToolResult{
      Content: []schema.CallToolResultContentElem{
        {
          Type: "text",
          Text: fmt.Sprintf("Error: %v", err),
        },
      },
      IsError: &isError,
    }, nil
  }

  return &schema.CallToolResult{
    Content: []schema.CallToolResultContentElem{
      {
        Type: "text",
        Text: output,
      },
    },
  }, nil
}


func (i *MyImplementer) Implements(method string) bool {
	switch method {
	case schema.MethodResourcesList, schema.MethodResourcesRead, schema.MethodSubscribe, schema.MethodUnsubscribe:
		return true
	}
	return false
}

func New() server.NewImplementer {
	return func(ctx context.Context, notifier transport.Notifier, logger logger.Logger, client client.Operations) server.Implementer {
		base := implementer.New(notifier, logger, client)
		return &MyImplementer{Base: base, fs: afs.New()}
	}
}

```

## Example: Filesystem Implementation

The project includes a filesystem implementer example that exposes go embed files through the MCP protocol. This example demonstrates how to create a custom implementer that allows browsing and reading files.

Usage:

```go


config := &fs.Config{
    BaseURL: "embed:///resources",
    Options: []storage.Option{
        resourceFS, // An embedded filesystem
    },
}

// Create the implementer
newImplementer := fs.New(config)

// Use with server
srv, _ := server.New(server.WithNewImplementer(newImplementer))
```

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


## Authentication

### OAuth 2.1 Authorization Code Flow with PKCE for MCP

https://modelcontextprotocol.io/specification/draft/basic/authorization

1. Initial Unauthorized Request: The client accesses the MCP resource without a token and receives a 401 Unauthorized with a WWW-Authenticate header pointing to metadata (.well-known/oauth-protected-resource).

2. Resource Metadata Discovery: The client fetches the .well-known/oauth-protected-resource document, learning which Authorization Server(s) are trusted.

3. Authorization Server Metadata Discovery: Using the metadata, the client fetches the .well-known/oauth-authorization-server document to get the auth and token endpoints.

4. User Login with PKCE (OAuth 2.1 Authorization): The client redirects the user to log in, using the PKCE-enhanced Authorization Code flow.

5. Token Request and Response: After the user authorizes, the client exchanges the code for an access token.

6. Authorized Request to MCP: The client retries the protected request, now with a Bearer token in the Authorization header.

7. Successful Resource Response: If the token is valid, the MCP server responds with the requested resource.

The flow adheres to RFC 9728 and RFC 8414, ensuring standards-based integration with identity providers


#### HTTP/SSE MCP Server - Global Protected Resource

```go
package main

import (
  "context"
  "github.com/viant/mcp/protocol/server"
  "github.com/viant/mcp/protocol/server/auth"
  "github.com/viant/mcp/schema"
)

func ServerAuthUsage() error {

  var implemener *MyImplementer
  var options = []server.Option{
    server.WithAuthConfig(&auth.Config{
      ExcludeURI: "/sse",  //experimental initial SSE GET request is not protected as it's open all the time, there would be not way to refresh token, all POST are protected
      Global: &meta.ProtectedResourceMetadata{
        Resource: "https://me",
        AuthorizationServers: []string{
          "https://oauth.example.com/",
        },
      },
    }),
    server.WithNewImplementer(newImplementer),
    server.WithImplementation(schema.Implementation{"MCP Terminal", "0.1"}),
    server.WithCapabilities(schema.ServerCapabilities{
      Resources: &schema.ServerCapabilitiesResources{},
    }),
  }
  srv, err := server.New(options...)
  if err != nil {
    return err
  }
  ctx := context.Background()
  endpoint := srv.HTTP(ctx, ":80")
  return endpoint.ListenAndServe()
}

```


#### HTTP/SSE MCP Client - Global Protected Resource


### Usage

Below is a simple example demonstrating how to wire the OAuth2 components into an MCP client.

```go
package main
import (
    "context"
    "net/http"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/protocol/client/auth/flow"
    "github.com/viant/mcp/protocol/client/auth/mock"
    "github.com/viant/mcp/protocol/client/auth/store"
    "github.com/viant/mcp/protocol/client/auth/transport"
    "github.com/viant/mcp/protocol/client"
    "github.com/viant/mcp/schema"
)

func ExampleOAuth2Client() {
    ctx := context.Background()


    issuer := "http://oauth.example.com"
    oAuth2Client := client.NewConfig("client_id", "client_secret", oauth2.Endpoint{
    AuthURL:   url.Join(issuer, "authorize"),
    TokenURL:  url.Join(issuer, "token"),
    AuthStyle: oauth2.AuthStyleInHeader,
    }, "read", "write")
	
// 1. Create an in-memory store with client credentials.
    aStore := store.NewMemoryStore(store.WithClient(oAuth2Client))

    // 2. Build an OAuth2-enabled RoundTripper using the Browser flow.
    rt, err := transport.New(
        transport.WithStore(aStore),
        transport.WithAuthFlow(flow.NewBrowserFlow()),
    )
    if err != nil {
        // handle error
    }

    // 3. Use the RoundTripper in a standard HTTP client.
    httpClient := &http.Client{Transport: rt}

    // 4. Create an SSE transport that uses the OAuth2-enabled HTTP client.
    sseTransport, err := sse.New(ctx, "http://mcpserver/sse", sse.WithRPCHTTPClient(httpClient))
    if err != nil {
        // handle error
    }
    // 5. Instantiate the MCP client with the SSE transport.
    aClient := client.New("tester", "0.1", sseTransport, client.WithCapabilities(schema.ClientCapabilities{}))

    // 6. Use the client as usual; transport handles 401→OAuth2 flow automatically.
    initResult, err := aClient.Initialize(ctx)
    _ = initResult // use initialization result
    // ... further calls, e.g. aClient.ListTools(ctx, nil)
}
```





## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [Apache License 2.0](LICENSE).

## Credits

Author: Adrian Witas

This project is maintained by [Viant](https://github.com/viant).
