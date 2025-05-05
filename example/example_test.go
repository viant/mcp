package example

import (
	"context"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server"
	"log"
)

func Usage_Example() {

	newImplementer := serverproto.WithDefaultImplementer(context.Background(), func(implementer *serverproto.DefaultImplementer) {
		// Register a simple resource
		implementer.RegisterResource(schema.Resource{Name: "hello", Uri: "/hello"},
			func(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
				return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Text: "Hello, world!"}}}, nil
			})

		type Addition struct {
			A int `json:"a"`
			B int `json:"b"`
		}
		// Register a simple calculator tool: adds two integers
		if err := serverproto.RegisterTool[*Addition](implementer, "add", "Add two integers", func(ctx context.Context, input *Addition) (*schema.CallToolResult, *jsonrpc.Error) {
			sum := input.A + input.B
			return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{{Text: fmt.Sprintf("%d", sum)}}}, nil
		}); err != nil {
			panic(err)
		}
	})

	srv, err := server.New(
		server.WithNewImplementer(newImplementer),
		server.WithImplementation(schema.Implementation{"default", "1.0"}),
		server.WithCapabilities(schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}}),
	)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	log.Fatal(srv.HTTP(context.Background(), ":4981").ListenAndServe())
}
