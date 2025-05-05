package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

type Handler struct {
	implementer client.Implementer
}

func (h *Handler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	if !h.implementer.Implements(request.Method) {
		response.Id = request.Id
		response.Jsonrpc = request.Jsonrpc
		response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method %s not found", request.Method), nil)
		return
	}
	switch request.Method {
	case schema.MethodRootsList:
		listResponse, err := h.ListRoots(ctx, request)
		h.setResponse(response, listResponse, err)
	case schema.MethodSamplingCreateMessage:
		createResponse, err := h.CreateMessageRequest(ctx, request)
		h.setResponse(response, createResponse, err)
	default:
		response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method %s not found", request.Method), nil)
	}
}

// OnNotification handles notification
func (h *Handler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	h.implementer.OnNotification(ctx, notification) //ignore
}

func (s *Handler) setResponse(response *jsonrpc.Response, result interface{}, rpcError *jsonrpc.Error) {
	if rpcError != nil {
		response.Error = rpcError
	}
	var err error
	response.Result, err = json.Marshal(result)
	if err != nil {
		response.Error = jsonrpc.NewInternalError(err.Error(), []byte{})
	}
}
