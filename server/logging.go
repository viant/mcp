package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// SetLevel handles the logging/setLevel method
func (h *Handler) SetLevel(ctx context.Context, request *jsonrpc.Request) (*schema.SetLevelResult, *jsonrpc.Error) {
	setLevelRequest := &schema.SetLevelRequest{Method: request.Method}
	if err := json.Unmarshal(request.Params, &setLevelRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	h.loggingLevel = setLevelRequest.Params.Level
	return &schema.SetLevelResult{}, nil
}
