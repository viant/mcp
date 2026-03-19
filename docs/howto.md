# Build a Basic MCP Server (Go)

This guide shows how to spin up a minimal MCP server in Go, register a typed tool, and understand how struct field tags are converted to JSON Schema for tools/list using mcp-protocol schema utilities.

- Server runtime: github.com/viant/mcp
- Protocol helpers: github.com/viant/mcp-protocol

## Minimal Server

    package main

    import (
        "context"
        "encoding/json"
        "log"

        "github.com/viant/jsonrpc"
        proto "github.com/viant/mcp-protocol/server"
        "github.com/viant/mcp-protocol/schema"
        "github.com/viant/mcp/server"
    )

    func main() {
        // Define a simple tool I/O
        type AddIn struct {
            A int // json:"a"
            B int // json:"b"
            Note *string // json:"note,omitempty" description:"Optional note"
        }
        type AddOut struct { Sum int /* json:"sum" */ }

        // Configure handler and register the tool
        newHandler := proto.WithDefaultHandler(context.Background(), func(h *proto.DefaultHandler) error {
            return proto.RegisterTool[*AddIn, *AddOut](
                h.Registry,
                "add",
                "Add two integers",
                func(ctx context.Context, in *AddIn) (*schema.CallToolResult, *jsonrpc.Error) {
                    data, _ := json.Marshal(&AddOut{Sum: in.A + in.B})
                    return &schema.CallToolResult{
                        Content: []schema.CallToolResultContentElem{{Text: string(data)}},
                    }, nil
                },
            )
        })

        srv, err := server.New(
            server.WithNewHandler(newHandler),
            server.WithImplementation(schema.Implementation{Name: "example", Version: "1.0"}),
        )
        if err != nil { log.Fatal(err) }

        // Choose a transport (see sections below)
        // Example: HTTP (SSE by default)
        log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
    }

Notes:
- proto.WithDefaultHandler provides a registry for tools/resources/prompts and default method handling.
- proto.RegisterTool[I,O] derives inputSchema and outputSchema from your Go types and wires a typed handler.
- For stdio transport (common for MCP), use `srv.Stdio(ctx).ListenAndServe()`.

## Start the Server

You can run the server over stdio, HTTP/SSE, or HTTP streamable.

### Stdio (typical for editor integrations)

    ctx := context.Background()
    stdioSrv := srv.Stdio(ctx)
    log.Fatal(stdioSrv.ListenAndServe())

This listens for JSON-RPC messages on stdin/stdout.

### HTTP (SSE)

    ctx := context.Background()
    httpSrv := srv.HTTP(ctx, ":4981")
    log.Fatal(httpSrv.ListenAndServe())

By default the HTTP transport uses Server-Sent Events (SSE).

### HTTP (Streamable)

    srv.UseStreamableHTTP(true)
    httpSrv := srv.HTTP(context.Background(), ":4981")
    log.Fatal(httpSrv.ListenAndServe())

Toggling `UseStreamableHTTP(true)` switches the HTTP handler to the streamable HTTP transport.

#### Endpoints and Routing

- Both transports are mounted by default:
  - SSE: `GET /sse` and `POST /message`
  - Streamable HTTP: `POST/GET /mcp`
- Root redirect can be enabled so `"/"` forwards to the active transport:
  - `WithRootRedirect(true)`
- To customize endpoints:
  - `WithStreamableURI("/api/mcp")`
  - `WithSSEURI("/api/sse")`
  - `WithSSEMessageURI("/api/rpc")`

```go
srv, _ := mcp.New(
  mcp.WithNewHandler(newHandler),
  mcp.WithStreamableURI("/api/mcp"),
  mcp.WithSSEURI("/api/sse"),
  mcp.WithSSEMessageURI("/api/rpc"),
  mcp.WithRootRedirect(true),
)
```

### Examples (in this repo)

Examples live under `example/` and can be executed with `go test ./example/...`:

- Tools: `example/tool` (used by `example/example_test.go`)
- Resources: `example/resource` and `example/fs`
- Custom handler: `example/custom`
- Auth flows: `example/auth/experimental`, `example/auth/percall`, `example/auth/term`

For a quick smoke test, run:

    go test ./example/...

## Calling Your Tool

From a client using github.com/viant/mcp adapter:

    // Build params directly from a typed input
    params, _ := schema.NewCallToolRequestParams("add", &AddIn{A: 2, B: 3})
    res, err := client.CallTool(ctx, params)
    // res.Content[0].Text contains a JSON string: {"sum":5}

Clients discover the tool and its schemas via tools/list.

## Create a Client

Connect from Go using SSE, streamable, or stdio transports.

### SSE client

    ctx := context.Background()
    sseTransport, _ := sse.New(ctx, "http://localhost:4981/sse")
    cli := client.New("Demo", "1.0", sseTransport)
    if _, err := cli.Initialize(ctx); err != nil { log.Fatal(err) }
    res, _ := cli.ListResources(ctx, nil)
    fmt.Println("resources:", len(res.Resources))

### Streamable client

    streamTransport, _ := streamable.New(ctx, "http://localhost:4981/")
    cli := client.New("Demo", "1.0", streamTransport)
    _, _ = cli.Initialize(ctx)

### Stdio client (spawned server)

    stdioTransport, _ := stdio.New("./your-mcp-server-binary",
        stdio.WithArguments("--flag1", "value"))
    cli := client.New("Demo", "1.0", stdioTransport)
    _, _ = cli.Initialize(ctx)

### Authenticated HTTP client (optional)

For OAuth2/OIDC-protected servers, create an authenticated `http.Client` and pass it to the transport (see README Authentication section for a full example):

    rt, _ := transport.New(
        transport.WithStore(myStore),
        transport.WithAuthFlow(flow.NewBrowserFlow()),
    )
    httpClient := &http.Client{Transport: rt}
    sseTransport, _ := sse.New(ctx, "https://secure.example.com/sse", sse.WithHttpClient(httpClient))
    cli := client.New("Demo", "1.0", sseTransport)
    _, _ = cli.Initialize(ctx)

## Add a Resource

Register a readable resource (and optional templates) that clients can list/read/subscribe to.

    // Inside WithDefaultHandler block
    h.RegisterResource(schema.Resource{
        Name: "hello",
        Uri:  "/hello",
        // MimeType: ptr("text/plain"), // optional
    }, func(ctx context.Context, req *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
        return &schema.ReadResourceResult{
            Contents: []schema.ReadResourceResultContentsElem{{
                Text: "Hello, world!",
                Uri:  req.Params.Uri,
            }},
        }, nil
    })

Notify subscribers when content changes by sending `resources/updated`:

    notif, _ := jsonrpc.NewNotification(
        schema.MethodNotificationResourceUpdated,
        map[string]string{"uri": "/hello"},
    )
    _ = h.Notifier.Send(ctx, notif)

## Add a Prompt

Expose reusable prompts that clients can list and resolve with arguments.

    welcome := &schema.Prompt{
        Name:        "welcome",
        Description: ptr("Greets a user by name"),
        Arguments: []schema.PromptArgument{
            {Name: "name", Required: ptr(true)},
        },
    }

    h.RegisterPrompts(welcome, func(ctx context.Context, p *schema.GetPromptRequestParams) (*schema.GetPromptResult, *jsonrpc.Error) {
        name := p.Arguments["name"]
        return &schema.GetPromptResult{
            Messages: []schema.PromptMessage{{
                Role:    schema.RoleAssistant,
                Content: schema.TextContent{Type: "text", Text: "Hello, " + name + "!"},
            }},
        }, nil
    })

## Struct Tags to JSON Schema Mapping

The mapping is implemented in mcp-protocol/schema/tool.go (see ToolInputSchema and ToolOutputSchema loaders). Key rules:

- Type mapping:
  - bool → type: boolean
  - integers (int, int32, uint64, …) → type: integer
  - floats → type: number
  - string → type: string
  - time.Time → type: string, format: date-time
  - []T and [N]T → type: array, items: schema(T)
  - map[string]interface{} → type: object, additionalProperties: true
  - map[string]T → type: object, additionalProperties: schema(T)
  - interface{} → unconstrained (open) schema object

- Pointers and nullability:
  - Pointer fields are marked nullable: true by default.
  - Inside arrays, pointer element types are not auto-marked nullable unless you override (see Nullable hook below).

- Required vs optional fields:
  - A field is added to the schema required list when BOTH are true:
    - it is not a pointer AND
    - it does not have omitempty in its json tag
  - Overrides:
    - required:true forces required
    - required:false OR presence of optional in the tag makes it optional

- json tag:
  - Renames the field; json:- excludes it.
  - omitempty influences required calculation as above.

- description tag:
  - Adds description to the field schema.

- format tag:
  - Sets format (for example: format:uri, format:email).

- choice:"val" tag (enum):
  - One or more choice:"..." tags on the field produce an enum array.
  - Example: Status string with json:"status", choice:"new", choice:"done" yields enum ["new","done"].

- internal tag:
  - If present and non-empty, the field is skipped entirely.

- Hooks (advanced):
  - The loaders accept options (StructToPropertiesOption) to customize behavior per field:
    - WithSkipFieldHook(fn) → exclude fields programmatically
    - WithIsRequiredHook(fn) → override required decision
    - WithFormatHook(fn) → compute format
    - WithNullableHook(fn) → force on/off nullable regardless of defaults
    - WithDescriptionHook(text) → injects an extra desc field with a fixed description

Example schema outcome for the AddIn type above:

    {
      "type": "object",
      "properties": {
        "a": {"type": "integer"},
        "b": {"type": "integer"},
        "note": {"type": "string", "nullable": true, "description": "Optional note"}
      },
      "required": ["a", "b"]
    }

## Alternate: Register With Explicit Schemas

If you need full control, build schemas explicitly and register with RegisterToolWithSchema:

    in := schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{
        "cmd": {"type": "string"},
    }}
    out := &schema.ToolOutputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{
        "code": {"type": "integer"},
    }}

    h.Registry.RegisterToolWithSchema(
        "shell", "Run a shell command", in, out,
        func(ctx context.Context, req *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
            // parse req.Params.Arguments as needed
            return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{{Text: "{\"code\":0}"}}}, nil
        },
    )

## Transport Options (summary)

- HTTP (SSE): `srv.HTTP(ctx, ":4981").ListenAndServe()`
- HTTP (Streamable): `srv.UseStreamableHTTP(true); srv.HTTP(ctx, ":4981").ListenAndServe()`
- Stdio: `srv.Stdio(ctx).ListenAndServe()`

That’s it—define typed I/O, register with the default handler, and the server advertises capabilities and schemas automatically for a smooth tool UX in MCP clients.
