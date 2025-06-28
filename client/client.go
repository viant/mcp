package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client/auth"
)

var errUninitialized = fmt.Errorf("clientHandler is not initialized")

type Client struct {
	capabilities    schema.ClientCapabilities
	info            schema.Implementation
	meta            map[string]any // Optional meta information to include in the InitializeResult
	protocolVersion string
	transport       transport.Transport // server version
	initialized     bool
	clientHandler   client.Handler
	authInterceptor *auth.Authorizer
}

func (c *Client) isInitialized() bool {
	return c.initialized
}

func (c *Client) Initialize(ctx context.Context) (*schema.InitializeResult, error) {
	params := &schema.InitializeRequestParams{
		Capabilities:    c.capabilities,
		ClientInfo:      c.info,
		ProtocolVersion: c.protocolVersion,
	}

	req, err := jsonrpc.NewRequest(schema.MethodInitialize, params)
	if err != nil {
		return nil, jsonrpc.NewInvalidRequest(err.Error(), nil)
	}
	response, err := c.transport.Send(ctx, req)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), req.Params)
	}
	var result schema.InitializeResult
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("failed to unmarshal InitializeResult: %v", err), nil)
	}
	err = c.transport.Notify(ctx, &jsonrpc.Notification{Method: schema.MethodNotificationInitialized})
	if err != nil {
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("failed to notify initialized: %v", err), nil)
	}
	c.initialized = true
	return &result, nil
}

func (c *Client) ListResourceTemplates(ctx context.Context, cursor *string) (*schema.ListResourceTemplatesResult, error) {
	params := &schema.ListResourceTemplatesRequestParams{Cursor: cursor}
	return send[schema.ListResourceTemplatesRequestParams, schema.ListResourceTemplatesResult](ctx, c, schema.MethodResourcesTemplatesList, params)
}

func (c *Client) ListResources(ctx context.Context, cursor *string) (*schema.ListResourcesResult, error) {
	params := &schema.ListResourcesRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListResourcesRequestParams, schema.ListResourcesResult](ctx, c, schema.MethodResourcesList, params)
}

func (c *Client) ListPrompts(ctx context.Context, cursor *string) (*schema.ListPromptsResult, error) {
	params := &schema.ListPromptsRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListPromptsRequestParams, schema.ListPromptsResult](ctx, c, schema.MethodPromptsList, params)
}

func (c *Client) ListTools(ctx context.Context, cursor *string) (*schema.ListToolsResult, error) {
	params := &schema.ListToolsRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListToolsRequestParams, schema.ListToolsResult](ctx, c, schema.MethodToolsList, params)
}

func (c *Client) ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams) (*schema.ReadResourceResult, error) {
	return send[schema.ReadResourceRequestParams, schema.ReadResourceResult](ctx, c, schema.MethodResourcesRead, params)
}

func (c *Client) GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams) (*schema.GetPromptResult, error) {
	return send[schema.GetPromptRequestParams, schema.GetPromptResult](ctx, c, schema.MethodPromptsGet, params)
}

func (c *Client) CallTool(ctx context.Context, params *schema.CallToolRequestParams) (*schema.CallToolResult, error) {
	return send[schema.CallToolRequestParams, schema.CallToolResult](ctx, c, schema.MethodToolsCall, params)
}

func (c *Client) Complete(ctx context.Context, params *schema.CompleteRequestParams) (*schema.CompleteResult, error) {
	return send[schema.CompleteRequestParams, schema.CompleteResult](ctx, c, schema.MethodComplete, params)
}

func (c *Client) Ping(ctx context.Context, params *schema.PingRequestParams) (*schema.PingResult, error) {
	return send[schema.PingRequestParams, schema.PingResult](ctx, c, schema.MethodPing, params)
}

func (c *Client) Subscribe(ctx context.Context, params *schema.SubscribeRequestParams) (*schema.SubscribeResult, error) {
	return send[schema.SubscribeRequestParams, schema.SubscribeResult](ctx, c, schema.MethodSubscribe, params)
}

func (c *Client) Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams) (*schema.UnsubscribeResult, error) {
	return send[schema.UnsubscribeRequestParams, schema.UnsubscribeResult](ctx, c, schema.MethodUnsubscribe, params)
}

func (c *Client) SetLevel(ctx context.Context, params *schema.SetLevelRequestParams) (*schema.SetLevelResult, error) {
	return send[schema.SetLevelRequestParams, schema.SetLevelResult](ctx, c, schema.MethodLoggingSetLevel, params)
}

// ----- New clientHandler operations (clientHandler side RPC methods) -----

func (c *Client) ListRoots(ctx context.Context, params *schema.ListRootsRequestParams) (*schema.ListRootsResult, error) {
	return send[schema.ListRootsRequestParams, schema.ListRootsResult](ctx, c, schema.MethodRootsList, params)
}

func (c *Client) CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams) (*schema.CreateMessageResult, error) {
	return send[schema.CreateMessageRequestParams, schema.CreateMessageResult](ctx, c, schema.MethodSamplingCreateMessage, params)
}

// Method name constants for experimental features which are not yet defined in mcp-protocol/schema.
// They are declared here to avoid compile-time dependency mismatch and will be removed once promoted upstream.
const (
	methodElicit            = "elicitation/create"
	methodInteractionCreate = "interaction/create"
)

func (c *Client) Elicit(ctx context.Context, params *schema.ElicitRequestParams) (*schema.ElicitResult, error) {
	return send[schema.ElicitRequestParams, schema.ElicitResult](ctx, c, methodElicit, params)
}

type versioner interface {
	ProtocolVersion() string
}

func New(name, version string, transport transport.Transport, options ...Option) *Client {
	ret := &Client{
		info:      *schema.NewImplementation(name, version),
		transport: transport,
	}
	for _, opt := range options {
		opt(ret)
	}

	if ret.protocolVersion == "" {
		if aVersioner, ok := ret.clientHandler.(versioner); ok {
			ret.protocolVersion = aVersioner.ProtocolVersion()
		} else {
			ret.protocolVersion = schema.LatestProtocolVersion
		}
	}
	if ret.clientHandler != nil {
		if ret.clientHandler.Implements(schema.MethodRootsList) {
			ret.capabilities.Roots = &schema.ClientCapabilitiesRoots{}
		}
		if ret.clientHandler.Implements(schema.MethodElicitationCreate) {
			ret.capabilities.Elicitation = map[string]interface{}{
				"supported": true,
			}
		}
		if ret.clientHandler.Implements(schema.MethodSamplingCreateMessage) {
			ret.capabilities.Sampling = make(map[string]interface{})
		}
	}
	return ret
}

// WithAuthInterceptor attaches an Authorizer to the clientHandler, enabling automatic retry
// of requests when receiving a 401 Unauthorized response. The interceptor's Intercept
// method will be called after each Send.
func WithAuthInterceptor(authorizer *auth.Authorizer) Option {
	return func(c *Client) {
		c.authInterceptor = authorizer
	}
}

func send[P any, R any](ctx context.Context, client *Client, method string, parameters *P) (*R, error) {
	if !client.isInitialized() { //ensure initialized
		return nil, jsonrpc.NewInternalError(errUninitialized.Error(), nil)
	}
	clientTransport := client.transport
	req, err := jsonrpc.NewRequest(method, parameters)
	if err != nil {
		return nil, jsonrpc.NewInvalidRequest(err.Error(), nil)
	}
	// Send initial request
	response, err := clientTransport.Send(ctx, req)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	// Optionally intercept 401 Unauthorized and retry with token
	if client.authInterceptor != nil {
		nextReq, interceptErr := client.authInterceptor.Intercept(ctx, req, response)
		if interceptErr != nil {
			return nil, jsonrpc.NewInternalError(interceptErr.Error(), nil)
		}
		if nextReq != nil {
			response, err = clientTransport.Send(ctx, nextReq)
			if err != nil {
				return nil, jsonrpc.NewInternalError(err.Error(), nil)
			}
		}
	}
	// Handle RPC error
	if response.Error != nil {
		return nil, response.Error
	}
	var result R
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		response.Error = jsonrpc.NewInternalError(err.Error(), nil)
	}

	return &result, nil
}
