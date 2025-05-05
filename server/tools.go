package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// ListTools handles the tools/list method
func (h *Handler) ListTools(ctx context.Context, request *jsonrpc.Request) (*schema.ListToolsResult, *jsonrpc.Error) {
	listToolsRequest := &schema.ListToolsRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &listToolsRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.implementer.ListTools(ctx, listToolsRequest)
}

// CallTool handles the tools/call method
func (h *Handler) CallTool(ctx context.Context, request *jsonrpc.Request) (*schema.CallToolResult, *jsonrpc.Error) {
	callToolRequest := &schema.CallToolRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &callToolRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.implementer.CallTool(ctx, callToolRequest)
}
