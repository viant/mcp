<!-- Focused server implementation guide: tools, resources, prompts -->
# MCP Server Guide: Tools, Resources, Prompts

This guide shows how to implement an MCP server in Go with a practical focus on three core surfaces exposed to clients: tools, resources, and prompts. It uses the `github.com/viant/mcp` server runtime together with protocol helpers from `github.com/viant/mcp-protocol`.

## Setup

Create a server using the default handler registry. This lets you register tools, resources, and prompts without writing a bespoke handler type. Example snippets below use a small helper to take addresses:

```go
// helper used in examples
func ptr[T any](v T) *T { return &v }
```

```go
package main

import (
    "context"
    "log"

    proto "github.com/viant/mcp-protocol/server"
    "github.com/viant/mcp-protocol/schema"
    "github.com/viant/mcp/server"
)

func main() {
    newHandler := proto.WithDefaultHandler(context.Background(), func(h *proto.DefaultHandler) error {
        // Registration happens here (tools/resources/prompts). See sections below.
        return nil
    })

    srv, err := server.New(
        server.WithNewHandler(newHandler),
        server.WithImplementation(schema.Implementation{"example", "tile", "1.0"}),
    )
    if err != nil {
        log.Fatal(err)
    }
    log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
}
```

On initialize, the default handler automatically advertises server capabilities based on what you register:
- Register at least one tool → capabilities.tools set
- Register at least one resource → capabilities.resources set
- Register at least one prompt → capabilities.prompts set

## Tools

Tools are typed functions the client can call. Use `RegisterTool` to derive JSON Schemas automatically from your input/output structs.

```go
type AddInput struct {
    A int `json:"a"`            // required (no pointer, no omitempty)
    B int `json:"b"`            // required
    Note *string `json:"note,omitempty"` // optional pointer
}

type AddOutput struct {
    Sum int `json:"sum"`
}

// Inside the WithDefaultHandler block:
if err := proto.RegisterTool[*AddInput, *AddOutput](
    h.Registry,
    "add",                       // tool name
    "Add two numbers",           // description
    func(ctx context.Context, in *AddInput) (*schema.CallToolResult, *jsonrpc.Error) {
        out := &AddOutput{Sum: in.A + in.B}
        // Return either text content or structuredContent per MCP.
        // Here we return a JSON-encoded text payload for simplicity.
        data, _ := json.Marshal(out)
        return &schema.CallToolResult{
            Content: []schema.CallToolResultContentElem{
                {Text: string(data)}, // schema.TextContent via alias
            },
        }, nil
    },
); err != nil {
    return err
}
```

Schema derivation notes:
- Required vs optional is inferred from struct shape and tags:
  - Non-pointer, no `omitempty` → required
  - Pointer or `omitempty` → optional
  - `required:"true"` forces required; `required:"false"` or `optional` marks optional
- Additional tags supported by the schema helpers: `description`, `format`, `choice:"val"`

Compatibility note: For clients with protocol version older than 2025-03-26, the default handler omits `outputSchema` from `tools/list` automatically.

Client tip: Use `schema.NewCallToolRequestParams(name, inputStruct)` to build request params from a typed input.

## Resources

Resources expose readable content by URI and can be subscribed to for change notifications.

### Register a resource

```go
// A simple text resource at /hello
h.RegisterResource(schema.Resource{
    Name: "hello",
    Uri:  "/hello",
    // MimeType: ptr("text/plain"), // optional
}, func(ctx context.Context, req *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
    return &schema.ReadResourceResult{
        Contents: []schema.ReadResourceResultContentsElem{{
            Text: "Hello, world!", // schema.TextResourceContents via alias
            Uri:  req.Params.Uri,
        }},
    }, nil
})
```

The default handler implements:
- `resources/list` using what you register
- `resources/read` by dispatching to your handler
- `resources/subscribe` and `resources/unsubscribe` with a built-in subscription map

### Notify resource updates

If the underlying content changes, notify subscribed clients. Use the handler notifier to emit `resources/updated`.

```go
// When a file changes, etc.:
notification, _ := jsonrpc.NewNotification(
    schema.MethodNotificationResourceUpdated,
    map[string]string{"uri": "/hello"},
)
_ = h.Notifier.Send(context.Background(), notification)
```

### Resource templates

You can advertise URI templates via `RegisterResourceTemplate` and `resources/templates/list`. This is useful to signal supported URI shapes (e.g., `file://{path}`). The default `ReadResource` uses exact URI dispatch; implement a custom `ReadResource` if you need pattern matching.

```go
h.RegisterResourceTemplate(schema.ResourceTemplate{
    Name:        "local file",
    UriTemplate: "file://{path}",
    Description: ptr("Read local files"),
}, func(ctx context.Context, req *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
    // Implement reading from req.Params.Uri
    // ...
    return &schema.ReadResourceResult{ /* ... */ }, nil
})
```

## Prompts

Prompts are server-provided prompt templates. Clients list them and request a fully rendered prompt via `prompts/get` with arguments.

### Register a prompt

```go
welcome := &schema.Prompt{
    Name:        "welcome",
    Description: ptr("Greets a user by name"),
    Arguments: []schema.PromptArgument{
        {Name: "name", Title: ptr("User Name"), Required: ptr(true)},
    },
}

h.RegisterPrompts(welcome, func(ctx context.Context, p *schema.GetPromptRequestParams) (*schema.GetPromptResult, *jsonrpc.Error) {
    name := p.Arguments["name"]
    return &schema.GetPromptResult{
        Description: ptr("Simple welcome prompt"),
        Messages: []schema.PromptMessage{
            {
                Role: schema.RoleUser,
                Content: schema.TextContent{Type: "text", Text: "Please greet the user warmly."},
            },
            {
                Role: schema.RoleAssistant,
                Content: schema.TextContent{Type: "text", Text: "Hello, " + name + "!"},
            },
        },
    }, nil
})
```

The default handler provides:
- `prompts/list` mapped to registered prompts
- `prompts/get` with required-argument validation based on your `Prompt.Argument` definitions

Supported `PromptMessage` content includes text, images, audio, and resource embeddings or links.

## Putting It Together

Combining everything in one handler:

```go
newHandler := proto.WithDefaultHandler(context.Background(), func(h *proto.DefaultHandler) error {
    // 1) Resource
    h.RegisterResource(schema.Resource{Name: "hello", Uri: "/hello"}, func(ctx context.Context, req *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
        return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Text: "Hello!", Uri: req.Params.Uri}}}, nil
    })

    // 2) Tool
    type EchoIn struct{ Msg string `json:"msg"` }
    type EchoOut struct{ Msg string `json:"msg"` }
    _ = proto.RegisterTool[*EchoIn, *EchoOut](h.Registry, "echo", "Echo a message", func(ctx context.Context, in *EchoIn) (*schema.CallToolResult, *jsonrpc.Error) {
        data, _ := json.Marshal(&EchoOut{Msg: in.Msg})
        return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{{Text: string(data)}}}, nil
    })

    // 3) Prompt
    prompt := &schema.Prompt{Name: "welcome", Arguments: []schema.PromptArgument{{Name: "name", Required: ptr(true)}}}
    h.RegisterPrompts(prompt, func(ctx context.Context, p *schema.GetPromptRequestParams) (*schema.GetPromptResult, *jsonrpc.Error) {
        return &schema.GetPromptResult{Messages: []schema.PromptMessage{{Role: schema.RoleAssistant, Content: schema.TextContent{Type: "text", Text: "Hello, " + p.Arguments["name"] + "!"}}}}, nil
    })
    return nil
})
```

## Tips

- Use `server.WithCORS`, `server.WithProtocolVersion`, and HTTP auth middleware from `server/auth` when wiring `server.New`.
- For long-running changes (file watches, etc.), use `h.Notifier.Send(...)` to emit `resources/updated`.
- To test without a client, the `server.Adapter` can call your handler directly with typed requests.

---

References:
- Core runtime: `github.com/viant/mcp/server`
- Protocol + registries: `github.com/viant/mcp-protocol/server` and `github.com/viant/mcp-protocol/schema`
- Examples: see the `example` directory in this repo
