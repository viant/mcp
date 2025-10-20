package mcp

import (
	"context"
	"github.com/viant/afs/url"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/client/auth/store"
	mcpserver "github.com/viant/mcp/server"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"net/http"
	"sync"

	"bytes"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	authtransport "github.com/viant/mcp/client/auth/transport"

	sse "github.com/viant/jsonrpc/transport/client/http/sse"
	streamable "github.com/viant/jsonrpc/transport/client/http/streamable"

	stdiosrv "github.com/viant/jsonrpc/transport/server/stdio"

	protoClient "github.com/viant/mcp-protocol/client"
	protologger "github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protoserver "github.com/viant/mcp-protocol/server"
)

// opsHandler adapts protoClient.Operations to proto client.Handler by forwarding notifications.
type opsHandler struct {
	protoClient.Operations
}

func (h *opsHandler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	_ = h.Notify(ctx, notification)
}

// dynamicHandler allows swapping the underlying handler at runtime.
type dynamicHandler struct {
	mu    sync.RWMutex
	inner transport.Handler
}

func (d *dynamicHandler) SetInner(h transport.Handler) {
	d.mu.Lock()
	d.inner = h
	d.mu.Unlock()
}

func (d *dynamicHandler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	d.mu.RLock()
	h := d.inner
	d.mu.RUnlock()
	if h == nil {
		response.Error = jsonrpc.NewMethodNotFound("handler not ready", request.Params)
		return
	}
	h.Serve(ctx, request, response)
}

func (d *dynamicHandler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	d.mu.RLock()
	h := d.inner
	d.mu.RUnlock()
	if h != nil {
		h.OnNotification(ctx, notification)
	}
}

type Service struct {
	downstream transport.Transport
	// remoteHandler forwards upstream server->client RPCs to the current downstream client connection.
	remoteHandler *dynamicHandler
	elicitator    *Elicitator
}

// clientImplementer proxies MCP server requests to the client endpoint.
type clientImplementer struct {
	endpoint   client.Interface
	downstream transport.Transport
	protocol   string
	// clientOps represents downstream client Operations (backchannel)
	clientOps protoClient.Operations
	// clientHandler adapts Operations to pclient.Handler for inbound upstream requests
	clientHandler protoClient.Handler
}

// Initialize proxies the initialize request to the client endpoint.
func (ci *clientImplementer) Initialize(ctx context.Context, init *schema.InitializeRequestParams, result *schema.InitializeResult) {
	// Populate downstream client capabilities for upstream awareness
	if ci.clientOps != nil {
		ci.clientOps.Init(ctx, &init.Capabilities)
	}
	ci.endpoint = client.New("proxy", "0.1", ci.downstream,
		client.WithProtocolVersion(init.ProtocolVersion),
		client.WithClientHandler(ci.clientHandler),
		client.WithCapabilities(schema.ClientCapabilities{Experimental: map[string]map[string]interface{}{}}))
	res, err := ci.endpoint.Initialize(ctx)
	if err != nil {
		return
	}
	*result = *res

}

// ListResources proxies the resources/list request.
func (ci *clientImplementer) ListResources(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListResourcesRequest]) (*schema.ListResourcesResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListResources(ctx, request.Request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListResourceTemplates proxies the resources/templates/list request.
func (ci *clientImplementer) ListResourceTemplates(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListResourceTemplatesRequest]) (*schema.ListResourceTemplatesResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListResourceTemplates(ctx, request.Request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ReadResource proxies the resources/read request.
func (ci *clientImplementer) ReadResource(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ReadResourceRequest]) (*schema.ReadResourceResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ReadResource(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Subscribe proxies the resources/subscribe request.
func (ci *clientImplementer) Subscribe(ctx context.Context, request *jsonrpc.TypedRequest[*schema.SubscribeRequest]) (*schema.SubscribeResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Subscribe(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Unsubscribe proxies the resources/unsubscribe request.
func (ci *clientImplementer) Unsubscribe(ctx context.Context, request *jsonrpc.TypedRequest[*schema.UnsubscribeRequest]) (*schema.UnsubscribeResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Unsubscribe(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListPrompts proxies the prompts/list request.
func (ci *clientImplementer) ListPrompts(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListPromptsRequest]) (*schema.ListPromptsResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListPrompts(ctx, request.Request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// GetPrompt proxies the prompts/get request.
func (ci *clientImplementer) GetPrompt(ctx context.Context, request *jsonrpc.TypedRequest[*schema.GetPromptRequest]) (*schema.GetPromptResult, *jsonrpc.Error) {
	res, err := ci.endpoint.GetPrompt(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListTools proxies the tools/list request.
func (ci *clientImplementer) ListTools(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListToolsRequest]) (*schema.ListToolsResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListTools(ctx, request.Request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// CallTool proxies the tools/call request.
func (ci *clientImplementer) CallTool(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	res, err := ci.endpoint.CallTool(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Complete proxies the complete request.
func (ci *clientImplementer) Complete(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CompleteRequest]) (*schema.CompleteResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Complete(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// SetLevel proxies the logging/setLevel request.
func (ci *clientImplementer) SetLevel(ctx context.Context, request *jsonrpc.TypedRequest[*schema.SetLevelRequest]) (*schema.SetLevelResult, *jsonrpc.Error) {
	res, err := ci.endpoint.SetLevel(ctx, &request.Request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// OnNotification handles incoming JSON-RPC notifications (no-op for bridge).
func (ci *clientImplementer) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	// no-op
}

// Implements indicates which methods are supported by this proxy.
func (ci *clientImplementer) Implements(method string) bool {
	switch method {
	case schema.MethodInitialize,
		schema.MethodPing,
		schema.MethodResourcesList,
		schema.MethodResourcesTemplatesList,
		schema.MethodResourcesRead,
		schema.MethodSubscribe,
		schema.MethodUnsubscribe,
		schema.MethodPromptsList,
		schema.MethodPromptsGet,
		schema.MethodToolsList,
		schema.MethodToolsCall,
		schema.MethodComplete,
		schema.MethodLoggingSetLevel:
		return true
	}
	return false
}

// New constructs a bridge Service. Provide either Config.Endpoint or Config.URL.
func New(ctx context.Context, cfg *Options) (*Service, error) {

	var err error
	dh := &dynamicHandler{}
	// Build optional authenticated HTTP client if configured
	var httpClient *http.Client
	if cfg.OAuth2ConfigURL != "" {
		var err error
		httpClient, err = buildAuthHTTPClient(ctx, cfg)
		if err != nil {
			return nil, err
		}
	} else if cfg.BackendForFrontend {
		authTransportOptions := []authtransport.Option{
			authtransport.WithBackendForFrontendAuth(),
			authtransport.WithGlobalResource(&authorization.Authorization{
				UseIdToken:                true,
				ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{AuthorizationServers: []string{}},
			}),
		}
		if cfg.BackendForFrontendHeader != "" {
			authTransportOptions = append(authTransportOptions, authtransport.WithAuthorizationExchangeHeader(cfg.BackendForFrontendHeader))
		}
		rt, err := authtransport.New(authTransportOptions...)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Transport: rt}
	}

	// Autodetect remote transport (Streamable vs SSE)
	var downstream transport.Transport
	if isStreamable(ctx, cfg.URL, httpClient) {
		opts := []streamable.Option{streamable.WithHandler(dh)}
		if httpClient != nil {
			opts = append(opts, streamable.WithHTTPClient(httpClient))
		}
		downstream, err = streamable.New(ctx, cfg.URL, opts...)
		if err != nil {
			return nil, err
		}
	} else {
		opts := []sse.Option{sse.WithHandler(dh)}
		if httpClient != nil {
			opts = append(opts, sse.WithHttpClient(httpClient), sse.WithMessageHttpClient(httpClient))
		}
		downstream, err = sse.New(ctx, cfg.URL, opts...)
		if err != nil {
			return nil, err
		}
	}
	var el *Elicitator
	if cfg.ElicitatorEnabled {
		el = NewElicitator(cfg.ElicitatorListenAddr, cfg.ElicitatorOpenBrowser)
	}
	return &Service{downstream: downstream, remoteHandler: dh, elicitator: el}, nil
}

// isStreamable tests remote URL for Streamable HTTP transport by attempting a POST initialize handshake.
func isStreamable(ctx context.Context, endpoint string, httpClient *http.Client) bool {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"mcp-bridge","version":"1"},"capabilities":{},"protocolVersion":"2025-06-18"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true
	}
	// Legacy servers often return 404/405 for POST at SSE URL
	return false
}

func buildAuthHTTPClient(ctx context.Context, cfg *Options) (*http.Client, error) {
	if cfg.EncryptionKey != "" {
		cfg.OAuth2ConfigURL += "|" + cfg.EncryptionKey
	}
	auth := authorizer.New()
	oAuthConfig := &authorizer.OAuthConfig{ConfigURL: cfg.OAuth2ConfigURL}
	if err := auth.EnsureConfig(ctx, oAuthConfig); err != nil {
		return nil, err
	}
	aStore := store.NewMemoryStore(store.WithClientConfig(oAuthConfig.Config))

	issuer, _ := url.Base(oAuthConfig.Config.Endpoint.AuthURL, "https")

	var authTransportOptions = []authtransport.Option{
		authtransport.WithStore(aStore),
		authtransport.WithAuthFlow(flow.NewBrowserFlow()),
		authtransport.WithGlobalResource(&authorization.Authorization{
			UseIdToken: true,
			ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				AuthorizationServers: []string{issuer},
			},
		}),
	}
	roundTripper, err := authtransport.New(authTransportOptions...)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: roundTripper}, nil
}

// HTTP starts an HTTP/SSE server on the given address that proxies MCP JSON-RPC calls to the client endpoint.
func (s *Service) HTTP(ctx context.Context, addr string) (*http.Server, error) {
	// build a NewServer that forwards requests to the client.Interface
	NewServer := func(ctx context.Context, notifier transport.Notifier, logger protologger.Logger, clientOps protoClient.Operations) (protoserver.Handler, error) {
		var wrapped protoClient.Operations = clientOps
		if s.elicitator != nil {
			wrapped = &opsAugmented{Operations: clientOps, el: s.elicitator}
		}
		handler := &opsHandler{Operations: wrapped}
		s.remoteHandler.SetInner(client.NewHandler(handler))
		impl := &clientImplementer{downstream: s.downstream, clientOps: wrapped, clientHandler: handler}
		return impl, nil
	}
	// create a server with our proxy implementer
	srv, err := mcpserver.New(
		mcpserver.WithNewHandler(NewServer),
	)
	if err != nil {
		return nil, err
	}
	return srv.HTTP(ctx, addr), nil
}

// Stdio starts a JSON-RPC server over standard input/output that proxies MCP calls to the client endpoint.
func (s *Service) Stdio(ctx context.Context) (*stdiosrv.Server, error) {
	NewServer := func(ctx context.Context, notifier transport.Notifier, logger protologger.Logger, clientOps protoClient.Operations) (protoserver.Handler, error) {
		var wrapped protoClient.Operations = clientOps
		if s.elicitator != nil {
			wrapped = &opsAugmented{Operations: clientOps, el: s.elicitator}
		}
		handler := &opsHandler{Operations: wrapped}
		s.remoteHandler.SetInner(client.NewHandler(handler))
		impl := &clientImplementer{downstream: s.downstream, clientOps: wrapped, clientHandler: handler}
		return impl, nil
	}
	srv, err := mcpserver.New(
		mcpserver.WithNewHandler(NewServer),
	)
	if err != nil {
		return nil, err
	}
	return stdiosrv.New(ctx, srv.NewHandler), nil
}
