package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/internal/conv"
)

// ListTools handles the tools/list method
func (h *Handler) ListTools(ctx context.Context, request *jsonrpc.Request) (*schema.ListToolsResult, *jsonrpc.Error) {
	listToolsRequest := &schema.ListToolsRequest{Method: request.Method}
	if err := unmarshalOptionalParams(request.Params, &listToolsRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.ListTools(ctx, &jsonrpc.TypedRequest[*schema.ListToolsRequest]{Request: listToolsRequest, Id: uint64(id)})
}

// CallTool handles the tools/call method
func (h *Handler) CallTool(ctx context.Context, request *jsonrpc.Request) (*schema.CallToolResult, *jsonrpc.Error) {
	raw := bytes.TrimSpace(request.Params)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, jsonrpc.NewInvalidParamsError("tool parameters must be a JSON object", nil)
	}
	callToolRequest := &schema.CallToolRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &callToolRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	// Protocol-generated custom unmarshallers may use json.Unmarshal internally,
	// so an outer UseNumber decoder alone cannot preserve argument precision.
	// Retain their validation, then decode arguments directly from the original
	// wire bytes before any tool or schema validator observes rounded values.
	var exact struct {
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := conv.DecodeNumbers(request.Params, &exact); err != nil {
		return nil, jsonrpc.NewInvalidParamsError("invalid tool arguments", nil)
	}
	callToolRequest.Params.Arguments = exact.Arguments
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.CallTool(ctx, &jsonrpc.TypedRequest[*schema.CallToolRequest]{Request: callToolRequest, Id: uint64(id)})
}
