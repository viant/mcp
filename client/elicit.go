package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// Elicit handles the "elicitation/create" method.
func (h *Handler) Elicit(ctx context.Context, request *jsonrpc.Request) (*schema.ElicitResult, *jsonrpc.Error) {
	elicitReq := &schema.ElicitRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &elicitReq.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.implementer.Elicit(ctx, &elicitReq.Params)
}
