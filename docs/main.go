package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	proto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server"
)

func main() {
	// Define a simple tool I/O
	type AddIn struct {
		A    int     // json:"a"
		B    int     // json:"b"
		Note *string // json:"note,omitempty" description:"Optional note"
	}
	type AddOut struct {
		Sum int /* json:"sum" */
	}

	// Configure handler and register the tool
	newHandler := proto.WithDefaultHandler(context.Background(), func(h *proto.DefaultHandler) error {
		return proto.RegisterTool[*AddIn, *AddOut](
			h.Registry,
			"add",
			"Add two integers",
			func(ctx context.Context, in *AddIn) (*schema.CallToolResult, *jsonrpc.Error) {
				data, _ := json.Marshal(&AddOut{Sum: in.A + in.B})
				structured := map[string]interface{}{}
				if err := json.Unmarshal(data, &structured); err != nil {
					return nil, jsonrpc.NewInternalError(err.Error(), nil)
				}
				return &schema.CallToolResult{
					StructuredContent: structured,
					Content: []schema.CallToolResultContentElem{
						{Text: string(data), Type: "text"}},
				}, nil
			},
		)
	})

	srv, err := server.New(
		server.WithNewHandler(newHandler),
		server.WithImplementation(schema.Implementation{Name: "example", Version: "1.0"}),
	)
	if err != nil {
		log.Fatal(err)
	}

	srv.UseStreamableHTTP(true)
	log.Fatal(srv.HTTP(context.Background(), ":4987").ListenAndServe())
}
