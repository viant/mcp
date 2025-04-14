package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/internal/conv"
	"github.com/viant/mcp/schema"
)

// Handler represents handler
type Handler struct {
	transport.Notifier
	*Logger
	*Server
	clientInitialize *schema.InitializeRequestParams
	loggingLevel     schema.LoggingLevel
	implementer      Implementer
	initialized      bool
}

// Serve handles incoming JSON-RPC requests
func (h *Handler) Serve(parent context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	// Check for valid JSONRPC version
	if jsonrpc.Version != request.Jsonrpc {
		response.Error = jsonrpc.NewInvalidRequest("invalid JSON-RPC version", nil)
		return
	}
	switch request.Method {
	case schema.MethodInitialize, schema.MethodPing:
	case schema.MethodLoggingSetLevel:
		if !h.initialized {
			response.Error = jsonrpc.NewInvalidRequest("client not initialized", nil)
			return
		}
	default:
		if !h.initialized {
			response.Error = jsonrpc.NewInvalidRequest("client not initialized", nil)
			return
		}
		if !h.implementer.Implements(request.Method) {
			response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method: %v not found", request.Method), request.Params)
			return
		}
	}

	id := conv.AsInt(request.Id)
	ctx, cancel := context.WithCancel(parent)
	activeContext, ctx := newActiveContext(ctx, cancel, request)
	h.activeContexts.Put(id, activeContext)
	defer h.cancelOperation(id)
	switch request.Method {
	case schema.MethodInitialize:
		result, err := h.Initialize(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodPing:
		result, err := h.Ping(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodResourcesList:
		result, err := h.ListResources(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodResourcesTemplatesList:
		result, err := h.ListResourceTemplates(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodResourcesRead:
		result, err := h.ReadResource(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodPromptsList:
		result, err := h.ListPrompts(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodPromptsGet:
		result, err := h.GetPrompt(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodToolsList:
		result, err := h.ListTools(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodToolsCall:
		result, err := h.CallTool(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodComplete:
		result, err := h.Complete(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodSubscribe:
		result, err := h.Subscribe(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodUnsubscribe:
		result, err := h.Unsubscribe(ctx, request)
		h.setResponse(response, result, err)
	case schema.MethodLoggingSetLevel:
		result, err := h.SetLevel(ctx, request)
		h.setResponse(response, result, err)
	default:
		response.Error = jsonrpc.NewMethodNotFound(fmt.Sprintf("method: %v not found", request.Method), request.Params)
	}
}

func (h *Handler) setResponse(response *jsonrpc.Response, result interface{}, rpcError *jsonrpc.Error) {
	if rpcError != nil {
		response.Error = rpcError
	}
	var err error
	response.Result, err = json.Marshal(result)
	if err != nil {
		response.Error = jsonrpc.NewInternalError(err.Error(), []byte{})
	}
}

// OnNotification handles incoming JSON-RPC notifications
func (h *Handler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	// Handle notifications if needed
	switch notification.Method {
	case schema.MethodNotificationCancel:
		h.Cancel(ctx, notification)
	case schema.MethodNotificationInitialized:
		h.initialized = true
		return
	}
	h.implementer.OnNotification(ctx, notification)
}
