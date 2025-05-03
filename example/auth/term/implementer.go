package term

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/gosh"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/implementer"
	"github.com/viant/mcp/logger"
	"github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/protocol/server"
	"github.com/viant/mcp/schema"
)

type (
	Implementer struct {
		*implementer.Base
		term *gosh.Service
	}
)

func (i *Implementer) ListTools(_ context.Context, _ *schema.ListToolsRequest) (*schema.ListToolsResult, *jsonrpc.Error) {
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

func (i *Implementer) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {

	token := ctx.Value(schema.AuthTokenKey)
	if token != nil {
		fmt.Printf("token: %+v\n", token)
	}

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

func (i *Implementer) Implements(method string) bool {
	switch method {
	case schema.MethodToolsList, schema.MethodToolsCall:
		return true
	}
	return false
}

func New(term *gosh.Service) server.NewImplementer {
	return func(_ context.Context, notifier transport.Notifier, logger logger.Logger, client client.Operations) server.Implementer {
		base := implementer.New(notifier, logger, client)
		return &Implementer{
			Base: base,
			term: term,
		}
	}
}
