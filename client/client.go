package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	stdiotransport "github.com/viant/jsonrpc/transport/client/stdio"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client/auth"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

var errUninitialized = fmt.Errorf("clientHandler is not initialized")

type Client struct {
	capabilities    schema.ClientCapabilities
	info            schema.Implementation
	meta            map[string]any // Optional meta information to include in the InitializeResult
	protocolVersion string
	transport       transport.Transport // server version
	initialized     bool
	clientHandler   pclient.Handler
	authInterceptor *auth.Authorizer

	// reconnect builds new transport and re-initialises handshake when the underlying session is lost.
	reconnect func(ctx context.Context) (transport.Transport, error)

	// background pinger
	pingInterval time.Duration
	pingStop     chan struct{}
	pingWG       sync.WaitGroup
}

func (c *Client) isInitialized() bool {
	return c.initialized
}

func (c *Client) Initialize(ctx context.Context, options ...RequestOption) (*schema.InitializeResult, error) {
	params := &schema.InitializeRequestParams{
		Capabilities:    c.capabilities,
		ClientInfo:      c.info,
		ProtocolVersion: c.protocolVersion,
	}

	req, err := jsonrpc.NewRequest(schema.MethodInitialize, params)
	if err != nil {
		return nil, jsonrpc.NewInvalidRequest(err.Error(), nil)
	}
	if ro := NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			// For stdio transport, inject _meta; for HTTP transports, use Bearer header only.
			if isStdio(c.transport) {
				req = withAuthMeta(req, ro.StringToken)
			}
			ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, ro.StringToken)
		}
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
	// Start background pinger if configured
	c.startPinger()
	return &result, nil
}

func (c *Client) ListResourceTemplates(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListResourceTemplatesResult, error) {
	params := &schema.ListResourceTemplatesRequestParams{Cursor: cursor}
	return send[schema.ListResourceTemplatesRequestParams, schema.ListResourceTemplatesResult](ctx, c, schema.MethodResourcesTemplatesList, params, options...)
}

func (c *Client) ListResources(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListResourcesResult, error) {
	params := &schema.ListResourcesRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListResourcesRequestParams, schema.ListResourcesResult](ctx, c, schema.MethodResourcesList, params, options...)
}

func (c *Client) ListPrompts(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListPromptsResult, error) {
	params := &schema.ListPromptsRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListPromptsRequestParams, schema.ListPromptsResult](ctx, c, schema.MethodPromptsList, params, options...)
}

func (c *Client) ListTools(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListToolsResult, error) {
	params := &schema.ListToolsRequestParams{
		Cursor: cursor,
	}
	return send[schema.ListToolsRequestParams, schema.ListToolsResult](ctx, c, schema.MethodToolsList, params, options...)
}

func (c *Client) ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams, options ...RequestOption) (*schema.ReadResourceResult, error) {
	return send[schema.ReadResourceRequestParams, schema.ReadResourceResult](ctx, c, schema.MethodResourcesRead, params, options...)
}

func (c *Client) GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams, options ...RequestOption) (*schema.GetPromptResult, error) {
	return send[schema.GetPromptRequestParams, schema.GetPromptResult](ctx, c, schema.MethodPromptsGet, params, options...)
}

func (c *Client) CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...RequestOption) (*schema.CallToolResult, error) {
	return send[schema.CallToolRequestParams, schema.CallToolResult](ctx, c, schema.MethodToolsCall, params, options...)
}

func (c *Client) Complete(ctx context.Context, params *schema.CompleteRequestParams, options ...RequestOption) (*schema.CompleteResult, error) {
	return send[schema.CompleteRequestParams, schema.CompleteResult](ctx, c, schema.MethodComplete, params, options...)
}

func (c *Client) Ping(ctx context.Context, params *schema.PingRequestParams, options ...RequestOption) (*schema.PingResult, error) {
	return send[schema.PingRequestParams, schema.PingResult](ctx, c, schema.MethodPing, params, options...)
}

func (c *Client) Subscribe(ctx context.Context, params *schema.SubscribeRequestParams, options ...RequestOption) (*schema.SubscribeResult, error) {
	return send[schema.SubscribeRequestParams, schema.SubscribeResult](ctx, c, schema.MethodSubscribe, params, options...)
}

func (c *Client) Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams, options ...RequestOption) (*schema.UnsubscribeResult, error) {
	return send[schema.UnsubscribeRequestParams, schema.UnsubscribeResult](ctx, c, schema.MethodUnsubscribe, params, options...)
}

// reconnectAndInitialize attempts to rebuild underlying transport using reconnect callback.
// It returns error if reconnect function is not configured or initialization fails.
func (c *Client) reconnectAndInitialize(ctx context.Context) error {
	if c.reconnect == nil {
		return fmt.Errorf("reconnect is not configured")
	}
	newTransport, err := c.reconnect(ctx)
	if err != nil {
		return err
	}
	c.transport = newTransport
	c.initialized = false
	_, err = c.Initialize(ctx)
	return err
}

// startPinger launches a background goroutine that periodically sends MCP ping.
// It stops when Close() is called. If ping fails, it attempts to reconnect.
func (c *Client) startPinger() {
	if c.pingInterval <= 0 {
		return
	}
	if c.pingStop != nil {
		return // already running
	}
	c.pingStop = make(chan struct{})
	c.pingWG.Add(1)
	go func() {
		defer c.pingWG.Done()
		ticker := time.NewTicker(c.pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Use a detached context to avoid tying ping to caller deadlines
				ctx := context.Background()
				if !c.isInitialized() {
					continue
				}
				_, err := c.Ping(ctx, &schema.PingRequestParams{})
				if err != nil {
					// Attempt reconnect and continue; ignore reconnect error here.
					_ = c.reconnectAndInitialize(ctx)
				}
			case <-c.pingStop:
				return
			}
		}
	}()
}

// Close stops background routines (like pinger). It does not close underlying transports.
func (c *Client) Close() {
	if c.pingStop != nil {
		close(c.pingStop)
		c.pingWG.Wait()
		c.pingStop = nil
	}
}

func (c *Client) SetLevel(ctx context.Context, params *schema.SetLevelRequestParams, options ...RequestOption) (*schema.SetLevelResult, error) {
	return send[schema.SetLevelRequestParams, schema.SetLevelResult](ctx, c, schema.MethodLoggingSetLevel, params, options...)
}

// ----- New clientHandler operations (clientHandler side RPC methods) -----

func (c *Client) ListRoots(ctx context.Context, params *schema.ListRootsRequestParams, options ...RequestOption) (*schema.ListRootsResult, error) {
	return send[schema.ListRootsRequestParams, schema.ListRootsResult](ctx, c, schema.MethodRootsList, params, options...)
}

func (c *Client) CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams, options ...RequestOption) (*schema.CreateMessageResult, error) {
	return send[schema.CreateMessageRequestParams, schema.CreateMessageResult](ctx, c, schema.MethodSamplingCreateMessage, params, options...)
}

// Method name constants for experimental features which are not yet defined in mcp-protocol/schema.
// They are declared here to avoid compile-time dependency mismatch and will be removed once promoted upstream.
const (
	methodElicit = "elicitation/create"
)

func (c *Client) Elicit(ctx context.Context, params *schema.ElicitRequestParams, options ...RequestOption) (*schema.ElicitResult, error) {
	return send[schema.ElicitRequestParams, schema.ElicitResult](ctx, c, methodElicit, params, options...)
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

func send[P any, R any](ctx context.Context, client *Client, method string, parameters *P, options ...RequestOption) (*R, error) {
	if !client.isInitialized() { //ensure initialized
		return nil, jsonrpc.NewInternalError(errUninitialized.Error(), nil)
	}
	// always use the latest transport on the client to avoid using
	// a stale transport after reconnect
	req, err := jsonrpc.NewRequest(method, parameters)
	if err != nil {
		return nil, jsonrpc.NewInvalidRequest(err.Error(), nil)
	}
	if ro := NewRequestOptions(options); ro != nil {
		if ro.RequestId != nil {
			req.Id = ro.RequestId
		}
		if ro.StringToken != "" {
			if isStdio(client.transport) {
				req = withAuthMeta(req, ro.StringToken)
			}
			ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, ro.StringToken)
		}
	}
	// Send initial request
	response, err := client.transport.Send(ctx, req)
	if err != nil {
		// Automatic session recovery – if the server has been restarted, the existing session can be lost.
		// In that case the transport returns an HTTP 404 error containing "session '<id>' not found".
		if strings.Contains(err.Error(), "session") && strings.Contains(err.Error(), "not found") {
			if recErr := client.reconnectAndInitialize(ctx); recErr == nil {
				// Construct fresh request to avoid duplicate id after successful reconnect
				req, _ = jsonrpc.NewRequest(method, parameters)
				if ro := NewRequestOptions(options); ro != nil {
					if ro.RequestId != nil {
						req.Id = ro.RequestId
					}
					if ro.StringToken != "" {
						if isStdio(client.transport) {
							req = withAuthMeta(req, ro.StringToken)
						}
						ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, ro.StringToken)
					}
				}
				response, err = client.transport.Send(ctx, req)
			} else {
				// if reconnect failed, propagate original error for visibility
				return nil, jsonrpc.NewInternalError(recErr.Error(), nil)
			}
		}
		if err != nil {
			return nil, jsonrpc.NewInternalError(err.Error(), nil)
		}
	}
	// Optionally intercept 401 Unauthorized and retry with token
	if client.authInterceptor != nil {
		nextReq, interceptErr := client.authInterceptor.Intercept(ctx, req, response)
		if interceptErr != nil {
			return nil, jsonrpc.NewInternalError(interceptErr.Error(), nil)
		}
		if nextReq != nil {
			// use the current transport (may have changed due to reconnect)
			response, err = client.transport.Send(ctx, nextReq)
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

// withAuthMeta ensures that request.Params has `_meta.authorization.token` with the provided value.
func withAuthMeta(req *jsonrpc.Request, token string) *jsonrpc.Request {
	if token == "" {
		return req
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
	return req
}

// isStdio reports whether the transport is stdio-based (no HTTP layer).
func isStdio(t transport.Transport) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*stdiotransport.Client); ok {
		return true
	}
	return false
}
