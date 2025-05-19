package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// Initialize handles the initialize method
func (h *Handler) Initialize(ctx context.Context, request *jsonrpc.Request) (*schema.InitializeResult, *jsonrpc.Error) {
	initRequest := schema.InitializeRequest{Method: schema.MethodInitialize}
	if err := json.Unmarshal(request.Params, &initRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse %v", err), request.Params)
	}
	protoVersion := h.protocolVersion
	h.clientInitialize = &initRequest.Params
	result := schema.InitializeResult{
		ProtocolVersion: protoVersion,
		ServerInfo:      h.info,
		Capabilities:    schema.ServerCapabilities{},
		Instructions:    h.instructions,
	}
	h.implementer.Initialize(ctx, h.clientInitialize, &result)
	return &result, nil
}

// Ping handles the ping method
func (h *Handler) Ping(ctx context.Context, request *jsonrpc.Request) (*schema.PingResult, *jsonrpc.Error) {
	pingRequest := schema.PingRequest{Method: schema.MethodPing}
	if err := json.Unmarshal(request.Params, &pingRequest.Params); err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), request.Params)
	}
	result := schema.PingResult{}
	return &result, nil
}
