package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// ListResources handles the resources/list method
func (h *Handler) ListResources(ctx context.Context, request *jsonrpc.Request) (*schema.ListResourcesResult, *jsonrpc.Error) {
	listResourcesRequest := &schema.ListResourcesRequest{Method: schema.MethodResourcesList}
	if err := json.Unmarshal(request.Params, &listResourcesRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.server.ListResources(ctx, listResourcesRequest)
}

// ListResourceTemplates handles the resources/templates/list method
func (h *Handler) ListResourceTemplates(ctx context.Context, request *jsonrpc.Request) (*schema.ListResourceTemplatesResult, *jsonrpc.Error) {
	listTemplatesRequest := &schema.ListResourceTemplatesRequest{Method: schema.MethodResourcesTemplatesList}
	if err := json.Unmarshal(request.Params, &listTemplatesRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}

	return h.server.ListResourceTemplates(ctx, listTemplatesRequest)
}

// ReadResource handles the resources/read method
func (h *Handler) ReadResource(ctx context.Context, request *jsonrpc.Request) (*schema.ReadResourceResult, *jsonrpc.Error) {
	readRequest := &schema.ReadResourceRequest{Method: schema.MethodResourcesRead}
	if err := json.Unmarshal(request.Params, &readRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.server.ReadResource(ctx, readRequest)
}

// Subscribe handles the resources/subscribe method
func (h *Handler) Subscribe(ctx context.Context, request *jsonrpc.Request) (*schema.SubscribeResult, *jsonrpc.Error) {
	subscribeRequest := &schema.SubscribeRequest{Method: schema.MethodSubscribe}
	if err := json.Unmarshal(request.Params, &subscribeRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.server.Subscribe(ctx, subscribeRequest)
}

// Unsubscribe handles the resources/unsubscribe method
func (h *Handler) Unsubscribe(ctx context.Context, request *jsonrpc.Request) (*schema.UnsubscribeResult, *jsonrpc.Error) {
	unsubscribeRequest := &schema.UnsubscribeRequest{Method: schema.MethodUnsubscribe}
	if err := json.Unmarshal(request.Params, &unsubscribeRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	return h.server.Unsubscribe(ctx, unsubscribeRequest)
}
