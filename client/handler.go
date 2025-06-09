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
	client client.Client
}

func (h *Handler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {

	if !h.client.Implements(request.Method) {
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
	case schema.MethodElicitationCreate:
		elicitResponse, err := h.Elicit(ctx, request)
		h.setResponse(response, elicitResponse, err)
	case schema.MethodInteractionCreate:
		uiResponse, err := h.CreateUserInteractionRequest(ctx, request)
		h.setResponse(response, uiResponse, err)
	default:
		response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method %s not found", request.Method), nil)
	}
}

func (s *Handler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	s.client.OnNotification(ctx, notification)
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

// NewHandler create client handler
func NewHandler(client client.Client) *Handler {
	return &Handler{client: client}
}
