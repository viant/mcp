# Build a Basic MCP Server with Tool Calls (Go)

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

        // HTTP transport (use stdio for typical MCP runtime integration)
        log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
    }

Notes:
- proto.WithDefaultHandler provides a registry for tools/resources/prompts and default method handling.
- proto.RegisterTool[I,O] derives inputSchema and outputSchema from your Go types and wires a typed handler.
- For stdio transport (common for MCP), use srv.Stdio(ctx, os.Stdin, os.Stdout) from this repo server/stdio.go.

## Calling Your Tool

From a client using github.com/viant/mcp adapter:

    // Build params directly from a typed input
    params, _ := schema.NewCallToolRequestParams("add", &AddIn{A: 2, B: 3})
    res, err := client.CallTool(ctx, params)
    // res.Content[0].Text contains a JSON string: {"sum":5}

Clients discover the tool and its schemas via tools/list.

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

## Transport Options

- HTTP: srv.HTTP(ctx, ":4981").ListenAndServe()
- Stdio: srv.Stdio(ctx, os.Stdin, os.Stdout) for typical MCP integrations

That’s it—define typed I/O, register with the default handler, and the server advertises schemas automatically for a smooth tool UX in MCP clients.

