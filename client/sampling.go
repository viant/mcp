package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// CreateMessageRequest handles the sampling/createMessage method
func (h *Handler) CreateMessageRequest(ctx context.Context, request *jsonrpc.Request) (*schema.CreateMessageResult, *jsonrpc.Error) {
	listRootRequest := &schema.CreateMessageRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &listRootRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.implementer.CreateMessage(ctx, &listRootRequest.Params)
}
