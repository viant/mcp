package tool

import (
	"context"
	"fmt"
	"github.com/viant/gosh"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
)

type TerminalCommand struct {
	Commands []string          `json:"commands"`
	Evn      map[string]string `json:"evn"`
}

type TerminalTool struct {
	service *gosh.Service
}

func (t *TerminalTool) Call(ctx context.Context, input *TerminalCommand) (*schema.CallToolResult, *jsonrpc.Error) {
	token := ctx.Value(authorization.TokenKey)
	if token != nil {
		fmt.Printf("token: %+v\n", token)
	}
	// Convert commands to a single string command
	cmdString := ""
	if len(input.Commands) > 0 {
		cmdString = input.Commands[0]
		for i := 1; i < len(input.Commands); i++ {
			cmdString += " && " + input.Commands[i]
		}
	}

	result := &schema.CallToolResult{}
	output, code, err := t.service.Run(ctx, cmdString)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), []byte(cmdString))
	}
	if code != 0 {
		isError := true
		result.IsError = &isError
	}
	result.Content = []schema.CallToolResultContentElem{
		{
			Text: output,
		},
	}
	return result, nil
}

func NewTool(service *gosh.Service) *TerminalTool {
	return &TerminalTool{
		service: service,
	}
}
