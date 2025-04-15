package implementer

import (
	"context"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/internal/collection"
	"github.com/viant/mcp/logger"
	"github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/schema"
)

type Base struct {
	Notifier         transport.Notifier
	Logger           logger.Logger
	Client           client.Operations
	ClientInitialize *schema.InitializeRequestParams
	Subscription     *collection.SyncMap[string, bool]
}

func (f *Base) Initialize(ctx context.Context, init *schema.InitializeRequestParams, result schema.InitializeResult) {
	f.ClientInitialize = init
}

func (f *Base) ListResources(ctx context.Context, request *schema.ListResourcesRequest) (*schema.ListResourcesResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) ListResourceTemplates(ctx context.Context, request *schema.ListResourceTemplatesRequest) (*schema.ListResourceTemplatesResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) ReadResource(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) Subscribe(ctx context.Context, request *schema.SubscribeRequest) (*schema.SubscribeResult, *jsonrpc.Error) {
	f.Subscription.Put(request.Params.Uri, true)
	return &schema.SubscribeResult{}, nil
}

func (f *Base) Unsubscribe(ctx context.Context, request *schema.UnsubscribeRequest) (*schema.UnsubscribeResult, *jsonrpc.Error) {
	f.Subscription.Delete(request.Params.Uri)
	return &schema.UnsubscribeResult{}, nil
}

func (f *Base) ListTools(ctx context.Context, request *schema.ListToolsRequest) (*schema.ListToolsResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) ListPrompts(ctx context.Context, request *schema.ListPromptsRequest) (*schema.ListPromptsResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) GetPrompt(ctx context.Context, request *schema.GetPromptRequest) (*schema.GetPromptResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) Complete(ctx context.Context, request *schema.CompleteRequest) (*schema.CompleteResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound(fmt.Sprintf("method %v not found", request.Method), nil)
}

func (f *Base) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {}

func (f *Base) Implements(method string) bool {
	return false
}

func New(notifier transport.Notifier, logger logger.Logger, client client.Operations) *Base {
	return &Base{
		Notifier:     notifier,
		Logger:       logger,
		Client:       client,
		Subscription: collection.NewSyncMap[string, bool](),
	}
}
