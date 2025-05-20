package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
)

// Adapter adapts a server Handler to implement the client.Interface
type Adapter struct {
	handler *Handler
}

// Initialize initializes the client
func (a *Adapter) Initialize(ctx context.Context) (*schema.InitializeResult, error) {
	params := &schema.InitializeRequestParams{}
	req, err := jsonrpc.NewRequest(schema.MethodInitialize, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.InitializeResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	// Send Initialized notification
	a.handler.OnNotification(ctx, &jsonrpc.Notification{Method: schema.MethodNotificationInitialized})

	return &result, nil
}

// ListResourceTemplates lists resource templates
func (a *Adapter) ListResourceTemplates(ctx context.Context, cursor *string) (*schema.ListResourceTemplatesResult, error) {
	params := &schema.ListResourceTemplatesRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodResourcesTemplatesList, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.ListResourceTemplatesResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// ListResources lists resources
func (a *Adapter) ListResources(ctx context.Context, cursor *string) (*schema.ListResourcesResult, error) {
	params := &schema.ListResourcesRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodResourcesList, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.ListResourcesResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// ListPrompts lists prompts
func (a *Adapter) ListPrompts(ctx context.Context, cursor *string) (*schema.ListPromptsResult, error) {
	params := &schema.ListPromptsRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodPromptsList, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.ListPromptsResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// ListTools lists tools
func (a *Adapter) ListTools(ctx context.Context, cursor *string) (*schema.ListToolsResult, error) {
	params := &schema.ListToolsRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodToolsList, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.ListToolsResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// ReadResource reads a resource
func (a *Adapter) ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams) (*schema.ReadResourceResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodResourcesRead, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.ReadResourceResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// GetPrompt gets a prompt
func (a *Adapter) GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams) (*schema.GetPromptResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodPromptsGet, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.GetPromptResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// CallTool calls a tool
func (a *Adapter) CallTool(ctx context.Context, params *schema.CallToolRequestParams) (*schema.CallToolResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodToolsCall, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.CallToolResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Complete completes a request
func (a *Adapter) Complete(ctx context.Context, params *schema.CompleteRequestParams) (*schema.CompleteResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodComplete, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.CompleteResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Ping pings the server
func (a *Adapter) Ping(ctx context.Context, params *schema.PingRequestParams) (*schema.PingResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodPing, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.PingResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Subscribe subscribes to a resource
func (a *Adapter) Subscribe(ctx context.Context, params *schema.SubscribeRequestParams) (*schema.SubscribeResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodSubscribe, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.SubscribeResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Unsubscribe unsubscribes from a resource
func (a *Adapter) Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams) (*schema.UnsubscribeResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodUnsubscribe, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.UnsubscribeResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// SetLevel sets the logging level
func (a *Adapter) SetLevel(ctx context.Context, params *schema.SetLevelRequestParams) (*schema.SetLevelResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodLoggingSetLevel, params)
	if err != nil {
		return nil, err
	}

	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)

	if response.Error != nil {
		return nil, response.Error
	}

	var result schema.SetLevelResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// NewAdapter creates a new adapter for the given handler
func NewAdapter(handler *Handler) *Adapter {
	return &Adapter{handler: handler}
}

// Ensure Adapter implements client.Interface
var _ client.Interface = (*Adapter)(nil)
