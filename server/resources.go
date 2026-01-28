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
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	jRequest := &jsonrpc.TypedRequest[*schema.ListResourcesRequest]{Id: uint64(id), Method: schema.MethodResourcesList, Request: listResourcesRequest}
	return h.handler.ListResources(ctx, jRequest)
}

// ListResourceTemplates handles the resources/templates/list method
func (h *Handler) ListResourceTemplates(ctx context.Context, request *jsonrpc.Request) (*schema.ListResourceTemplatesResult, *jsonrpc.Error) {
	listTemplatesRequest := &schema.ListResourceTemplatesRequest{Method: schema.MethodResourcesTemplatesList}
	if err := json.Unmarshal(request.Params, &listTemplatesRequest.PaginatedRequestParams); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	jRequest := &jsonrpc.TypedRequest[*schema.ListResourceTemplatesRequest]{Id: uint64(id), Method: schema.MethodResourcesTemplatesList, Request: listTemplatesRequest}
	return h.handler.ListResourceTemplates(ctx, jRequest)
}

// ReadResource handles the resources/read method
func (h *Handler) ReadResource(ctx context.Context, request *jsonrpc.Request) (*schema.ReadResourceResult, *jsonrpc.Error) {
	readRequest := &schema.ReadResourceRequest{Method: schema.MethodResourcesRead}
	if err := json.Unmarshal(request.Params, &readRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	jRequest := &jsonrpc.TypedRequest[*schema.ReadResourceRequest]{Id: uint64(id), Method: schema.MethodResourcesRead, Request: readRequest}
	return h.handler.ReadResource(ctx, jRequest)
}

// Subscribe handles the resources/subscribe method
func (h *Handler) Subscribe(ctx context.Context, request *jsonrpc.Request) (*schema.SubscribeResult, *jsonrpc.Error) {
	subscribeRequest := &schema.SubscribeRequest{Method: schema.MethodSubscribe}
	if err := json.Unmarshal(request.Params, &subscribeRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	jRequest := &jsonrpc.TypedRequest[*schema.SubscribeRequest]{Id: uint64(id), Method: schema.MethodSubscribe, Request: subscribeRequest}
	return h.handler.Subscribe(ctx, jRequest)
}

// Unsubscribe handles the resources/unsubscribe method
func (h *Handler) Unsubscribe(ctx context.Context, request *jsonrpc.Request) (*schema.UnsubscribeResult, *jsonrpc.Error) {
	unsubscribeRequest := &schema.UnsubscribeRequest{Method: schema.MethodUnsubscribe}
	if err := json.Unmarshal(request.Params, &unsubscribeRequest.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	jRequest := &jsonrpc.TypedRequest[*schema.UnsubscribeRequest]{Id: uint64(id), Method: schema.MethodUnsubscribe, Request: unsubscribeRequest}
	return h.handler.Unsubscribe(ctx, jRequest)
}
