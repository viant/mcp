package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
)

// Adapter adapts a handler Handler to implement the client.Interface
type Adapter struct {
	handler *Handler
}

// injectAuthMeta ensures request params carry _meta.authorization.token for server-side auth interceptors.
func injectAuthMeta(req *jsonrpc.Request, token string) {
	if token == "" {
		return
	}
	var params map[string]interface{}
	if len(req.Params) > 0 && string(req.Params) != "null" {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	var meta map[string]interface{}
	if v, ok := params["_meta"].(map[string]interface{}); ok {
		meta = v
	} else {
		meta = map[string]interface{}{}
		params["_meta"] = meta
	}
	var auth map[string]interface{}
	if v, ok := meta["authorization"].(map[string]interface{}); ok {
		auth = v
	} else {
		auth = map[string]interface{}{}
		meta["authorization"] = auth
	}
	auth["token"] = token
	if raw, err := json.Marshal(params); err == nil {
		req.Params = raw
	}
}

// ---- New client operations ----

// ListRoots proxies "roots/list" to the underlying handler.
func (a *Adapter) ListRoots(ctx context.Context, params *schema.ListRootsRequestParams, options ...client.RequestOption) (*schema.ListRootsResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodRootsList, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
	}
	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)
	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.ListRootsResult
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateMessage proxies "sampling/createMessage" to the underlying handler.
func (a *Adapter) CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams, options ...client.RequestOption) (*schema.CreateMessageResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodSamplingCreateMessage, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
	}
	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)
	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.CreateMessageResult
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

const (
	adapterMethodElicit            = "elicitation/create"
	adapterMethodInteractionCreate = "interaction/create"
)

// Elicit proxies "elicitation/create" to the underlying handler.
func (a *Adapter) Elicit(ctx context.Context, params *schema.ElicitRequestParams, options ...client.RequestOption) (*schema.ElicitResult, error) {
	req, err := jsonrpc.NewRequest(adapterMethodElicit, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
	}
	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)
	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.ElicitResult
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Initialize initializes the client
func (a *Adapter) Initialize(ctx context.Context, options ...client.RequestOption) (*schema.InitializeResult, error) {
	params := &schema.InitializeRequestParams{}
	req, err := jsonrpc.NewRequest(schema.MethodInitialize, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) ListResourceTemplates(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListResourceTemplatesResult, error) {
	params := &schema.ListResourceTemplatesRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodResourcesTemplatesList, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) ListResources(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListResourcesResult, error) {
	params := &schema.ListResourcesRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodResourcesList, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) ListPrompts(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListPromptsResult, error) {
	params := &schema.ListPromptsRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodPromptsList, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) ListTools(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListToolsResult, error) {
	params := &schema.ListToolsRequestParams{Cursor: cursor}
	req, err := jsonrpc.NewRequest(schema.MethodToolsList, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams, options ...client.RequestOption) (*schema.ReadResourceResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodResourcesRead, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			injectAuthMeta(req, ro.StringToken)
		}
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
func (a *Adapter) GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams, options ...client.RequestOption) (*schema.GetPromptResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodPromptsGet, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
func (a *Adapter) CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...client.RequestOption) (*schema.CallToolResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodToolsCall, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
func (a *Adapter) Complete(ctx context.Context, params *schema.CompleteRequestParams, options ...client.RequestOption) (*schema.CompleteResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodComplete, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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

// Ping pings the handler
func (a *Adapter) Ping(ctx context.Context, params *schema.PingRequestParams, options ...client.RequestOption) (*schema.PingResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodPing, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
func (a *Adapter) Subscribe(ctx context.Context, params *schema.SubscribeRequestParams, options ...client.RequestOption) (*schema.SubscribeResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodSubscribe, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
func (a *Adapter) Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams, options ...client.RequestOption) (*schema.UnsubscribeResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodUnsubscribe, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
func (a *Adapter) SetLevel(ctx context.Context, params *schema.SetLevelRequestParams, options ...client.RequestOption) (*schema.SetLevelResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodLoggingSetLevel, params)
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
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
