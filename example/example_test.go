package example

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server"
)

func Usage_Example() {

	newHandler := serverproto.WithDefaultHandler(context.Background(), func(server *serverproto.DefaultHandler) error {
		// Register a simple resource
		server.RegisterResource(schema.Resource{Name: "hello", Uri: "/hello"},
			func(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
				return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Text: "Hello, world!"}}}, nil
			})

		type Addition struct {
			A int `json:"a"`
			B int `json:"b"`
		}

		type Result struct {
			Result int `json:"acc"`
		}
		// Register a simple calculator tool: adds two integers
		if err := serverproto.RegisterTool[*Addition, *Result](server.Registry, "add", "Add two integers", func(ctx context.Context, input *Addition) (*schema.CallToolResult, *jsonrpc.Error) {
			sum := input.A + input.B
			out := &Result{Result: sum}
			data, err := json.Marshal(out)
			if err != nil {
				return nil, jsonrpc.NewInternalError(fmt.Sprintf("failed to marshal result: %v", err), nil)
			}
			return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{
				schema.TextContent{Text: string(data), Type: "text"},
			}}, nil
		}); err != nil {
			return err
		}
		return nil
	})

	srv, err := server.New(
		server.WithNewHandler(newHandler),
		server.WithImplementation(schema.Implementation{Name: "default", Version: "1.0"}),
	)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
}
