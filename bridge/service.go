package bridge

import (
	"context"
	"github.com/viant/afs/url"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"net/http"

	"github.com/viant/mcp/client"
	mcpserver "github.com/viant/mcp/server"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	authtransport "github.com/viant/mcp/client/auth/transport"

	sse "github.com/viant/jsonrpc/transport/client/http/sse"

	stdiosrv "github.com/viant/jsonrpc/transport/server/stdio"

	protoClient "github.com/viant/mcp-protocol/client"
	protologger "github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protoserver "github.com/viant/mcp-protocol/server"
)

type Service struct {
	client.Interface
}

// clientImplementer proxies MCP server requests to the client endpoint.
type clientImplementer struct {
	endpoint client.Interface
}

// Initialize proxies the initialize request to the client endpoint.
func (ci *clientImplementer) Initialize(ctx context.Context, init *schema.InitializeRequestParams, result *schema.InitializeResult) {
	res, err := ci.endpoint.Initialize(ctx)
	if err != nil {
		return
	}
	*result = *res
}

// ListResources proxies the resources/list request.
func (ci *clientImplementer) ListResources(ctx context.Context, request *schema.ListResourcesRequest) (*schema.ListResourcesResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListResources(ctx, request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListResourceTemplates proxies the resources/templates/list request.
func (ci *clientImplementer) ListResourceTemplates(ctx context.Context, request *schema.ListResourceTemplatesRequest) (*schema.ListResourceTemplatesResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListResourceTemplates(ctx, request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ReadResource proxies the resources/read request.
func (ci *clientImplementer) ReadResource(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ReadResource(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Subscribe proxies the resources/subscribe request.
func (ci *clientImplementer) Subscribe(ctx context.Context, request *schema.SubscribeRequest) (*schema.SubscribeResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Subscribe(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Unsubscribe proxies the resources/unsubscribe request.
func (ci *clientImplementer) Unsubscribe(ctx context.Context, request *schema.UnsubscribeRequest) (*schema.UnsubscribeResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Unsubscribe(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListPrompts proxies the prompts/list request.
func (ci *clientImplementer) ListPrompts(ctx context.Context, request *schema.ListPromptsRequest) (*schema.ListPromptsResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListPrompts(ctx, request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// GetPrompt proxies the prompts/get request.
func (ci *clientImplementer) GetPrompt(ctx context.Context, request *schema.GetPromptRequest) (*schema.GetPromptResult, *jsonrpc.Error) {
	res, err := ci.endpoint.GetPrompt(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// ListTools proxies the tools/list request.
func (ci *clientImplementer) ListTools(ctx context.Context, request *schema.ListToolsRequest) (*schema.ListToolsResult, *jsonrpc.Error) {
	res, err := ci.endpoint.ListTools(ctx, request.Params.Cursor)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// CallTool proxies the tools/call request.
func (ci *clientImplementer) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
	res, err := ci.endpoint.CallTool(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// Complete proxies the complete request.
func (ci *clientImplementer) Complete(ctx context.Context, request *schema.CompleteRequest) (*schema.CompleteResult, *jsonrpc.Error) {
	res, err := ci.endpoint.Complete(ctx, &request.Params)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return res, nil
}

// SetLevel proxies the logging/setLevel request.
func (ci *clientImplementer) SetLevel(ctx context.Context, request *schema.SetLevelRequest) (*schema.SetLevelResult, *jsonrpc.Error) {
	res, err := ci.endpoint.SetLevel(ctx, &request.Params)
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

	var transportOption []sse.Option
	if cfg.OAuth2ConfigURL != "" {
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
		transportOption = append(transportOption, sse.WithMessageHttpClient(&http.Client{Transport: roundTripper}))
	}
	aTransport, err := sse.New(ctx, cfg.URL, transportOption...)
	if err != nil {
		return nil, err
	}

	aClient := client.New("proxy", "0.1", aTransport,
		client.WithCapabilities(schema.ClientCapabilities{Experimental: make(schema.ClientCapabilitiesExperimental)}))
	return &Service{Interface: aClient}, nil
}

// HTTP starts an HTTP/SSE server on the given address that proxies MCP JSON-RPC calls to the client endpoint.
func (s *Service) HTTP(ctx context.Context, addr string) (*http.Server, error) {
	// build a newImplementer that forwards requests to the client.Interface
	newImplementer := func(ctx context.Context, notifier transport.Notifier, logger protologger.Logger, _ protoClient.Operations) (protoserver.Implementer, error) {
		impl := &clientImplementer{endpoint: s.Interface}
		return impl, nil
	}
	// create a server with our proxy implementer
	srv, err := mcpserver.New(
		mcpserver.WithNewImplementer(newImplementer),
	)
	if err != nil {
		return nil, err
	}
	return srv.HTTP(ctx, addr), nil
}

// Stdio starts a JSON-RPC server over standard input/output that proxies MCP calls to the client endpoint.
func (s *Service) Stdio(ctx context.Context) (*stdiosrv.Server, error) {
	newImplementer := func(ctx context.Context, notifier transport.Notifier, logger protologger.Logger, _ protoClient.Operations) (protoserver.Implementer, error) {
		impl := &clientImplementer{endpoint: s.Interface}
		return impl, nil
	}
	srv, err := mcpserver.New(
		mcpserver.WithNewImplementer(newImplementer),
	)
	if err != nil {
		return nil, err
	}
	return stdiosrv.New(ctx, srv.NewHandler), nil
}
