package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// CreateUserInteractionRequest handles the "interaction/create" method.
func (h *Handler) CreateUserInteractionRequest(ctx context.Context, request *jsonrpc.Request) (*schema.CreateUserInteractionResult, *jsonrpc.Error) {
	uiReq := &schema.CreateUserInteractionRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &uiReq.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.client.CreateUserInteraction(ctx, &uiReq.Params)
}
