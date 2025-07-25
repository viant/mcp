package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/jsonrpc"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

type Handler struct {
	handler pclient.Handler
}

func (h *Handler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	response.Id = request.Id
	response.Jsonrpc = request.Jsonrpc
	if !h.handler.Implements(request.Method) {
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
	case schema.MethodElicitationCreate:
		elicitResponse, err := h.Elicit(ctx, request)
		h.setResponse(response, elicitResponse, err)
	default:
		response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method %s not found", request.Method), nil)
	}
}

func (s *Handler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	s.handler.OnNotification(ctx, notification)
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

// NewHandler create clientHandler clientHandler
func NewHandler(handler pclient.Handler) *Handler {
	return &Handler{handler: handler}
}
