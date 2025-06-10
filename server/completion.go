package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// Complete handles the completion/complete method
func (h *Handler) Complete(ctx context.Context, request *jsonrpc.Request) (*schema.CompleteResult, *jsonrpc.Error) {
	completeRequest := &schema.CompleteRequest{Method: schema.MethodComplete}
	if err := json.Unmarshal(request.Params, &completeRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(err.Error(), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.Complete(ctx, &jsonrpc.TypedRequest[*schema.CompleteRequest]{Request: completeRequest, Id: uint64(id)})
}
