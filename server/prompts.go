package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// ListPrompts handles the prompts/list method
func (h *Handler) ListPrompts(ctx context.Context, request *jsonrpc.Request) (*schema.ListPromptsResult, *jsonrpc.Error) {
	listPromptsRequest := &schema.ListPromptsRequest{Method: schema.MethodPromptsList}
	if err := json.Unmarshal(request.Params, &listPromptsRequest.PaginatedRequestParamsInline); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %h", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.ListPrompts(ctx, &jsonrpc.TypedRequest[*schema.ListPromptsRequest]{Request: listPromptsRequest, Id: uint64(id)})
}

// GetPrompt handles the prompts/get method
func (h *Handler) GetPrompt(ctx context.Context, request *jsonrpc.Request) (*schema.GetPromptResult, *jsonrpc.Error) {
	getPromptRequest := &schema.GetPromptRequest{Method: schema.MethodPromptsGet}
	if err := json.Unmarshal(request.Params, &getPromptRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.GetPrompt(ctx, &jsonrpc.TypedRequest[*schema.GetPromptRequest]{Request: getPromptRequest, Id: uint64(id)})
}
