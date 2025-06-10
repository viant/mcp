package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// ListRoots handles the root/listRoots method
func (h *Handler) ListRoots(ctx context.Context, request *jsonrpc.Request) (*schema.ListRootsResult, *jsonrpc.Error) {
	listRootRequest := &schema.ListRootsRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &listRootRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return h.handler.ListRoots(ctx, &jsonrpc.TypedRequest[*schema.ListRootsRequest]{Request: listRootRequest, Id: uint64(id)})
}
