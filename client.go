package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/viant/jsonrpc/transport"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/http/streaming"

	"github.com/viant/jsonrpc/transport/client/stdio"

	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"

	"github.com/viant/mcp/client/auth"
	"github.com/viant/mcp/client/auth/store"
	authtransport "github.com/viant/mcp/client/auth/transport"

	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"

	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp/client"
)

// ClientOptions defines options for configuring an MCP client.
type ClientOptions struct {
	Name            string          `yaml:"name" json:"name"  short:"n" long:"name" description:"mcp name"`
	Version         string          `yaml:"version" json:"version"  short:"v" long:"version" description:"mcp version"`
	ProtocolVersion string          `yaml:"protocol" json:"protocol"  short:"p" long:"protocol" description:"mcp protocol"`
	Namespace       string          `yaml:"namespace" json:"namespace"  short:"N" long:"namespace" description:"mcp namespace"`
	Transport       ClientTransport `yaml:"transport" json:"transport"  short:"t" long:"transport" description:"mcp transport options"`
	Auth            *ClientAuth     `yaml:"auth" json:"auth"  short:"a" long:"auth" description:"mcp auth options"`
}

// ClientAuth defines authentication options for an MCP client.
type ClientAuth struct {
	OAuth2ConfigURL    []string `yaml:"oauth2ConfigURL" json:"oauth2ConfigURL"  short:"c" long:"config" description:"oauth2 config file"`
	EncryptionKey      string   `yaml:"encryptionKey" json:"encryptionKey"  short:"k" long:"key" description:"encryption key"`
	UseIdToken         bool     `yaml:"useIdToken" json:"useIdToken"`
	BackendForFrontend bool     `yaml:"backendForFrontend" json:"backendForFrontend"  short:"b" long:"backend-for-frontend" description:"use backend for frontend"`
}

// ClientTransport defines transport options for an MCP client.
type ClientTransport struct {
	Type                 string `yaml:"type" json:"type"  short:"T" long:"transport-type" description:"mcp transport type, e.g., stdio, sse, streaming" choice:"stdio" choice:"sse" choice:"streaming"`
	ClientTransportStdio `yaml:",inline"`
	ClientTransportHTTP  `yaml:",inline"`
}

// ClientTransportStdio defines options for a standard input/output transport for an MCP client.
type ClientTransportStdio struct {
	Command   string   `yaml:"command" json:"command"  short:"C" long:"command" description:"mcp command"`
	Arguments []string `yaml:"arguments" json:"arguments"  short:"A" long:"arguments" description:"mcp command arguments"`
}

// ClientTransportHTTP defines options for a server-sent events transport for an MCP client.
type ClientTransportHTTP struct {
	URL string `yaml:"url" json:"url"  short:"u" long:"url" description:"mcp url"`
}

func (c *ClientOptions) Init() {
	if c.Name == "" {
		c.Name = "MCPClient"
		c.Version = "0.1"
	}
}

// NewClient creates an MCP client with transport and authorization configured via ClientOptions.
func NewClient(client protoclient.Client, options *ClientOptions) (*client.Client, error) {
	ctx := context.Background()
	rpcTransport, authRT, err := options.getTransport(ctx, client)
	if err != nil {
		return nil, err
	}

	opts := options.Options(authRT)
	opts = append(opts, client.WithImplementer(client))

	cli := client.New(options.Name, options.Version, rpcTransport, opts...)
	if _, err := cli.Initialize(ctx); err != nil {
		return nil, err
	}
	return cli, nil
}

// getTransport constructs a JSON-RPC transport based on ClientOptions.Transport and authentication settings.
func (c *ClientOptions) getTransport(ctx context.Context, mcpClient protoclient.Client) (transport.Transport, *authtransport.RoundTripper, error) {
	var httpClient *http.Client
	var authRT *authtransport.RoundTripper
	if c.Auth != nil {
		if c.Auth.BackendForFrontend {
			transportOpts := []authtransport.Option{authtransport.WithBackendForFrontendAuth()}
			if c.Auth.UseIdToken {
				transportOpts = append(transportOpts, authtransport.WithGlobalResource(&authorization.Authorization{
					UseIdToken:                c.Auth.UseIdToken,
					ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{AuthorizationServers: []string{}},
				}))
			}
			rt, err := authtransport.New(transportOpts...)
			if err != nil {
				return nil, nil, err
			}
			authRT = rt
			httpClient = &http.Client{Transport: rt}
		} else if len(c.Auth.OAuth2ConfigURL) > 0 {
			var err error
			httpClient, err = c.getOAuthHTTPClient(ctx)
			if err != nil {
				return nil, nil, err
			}
			if rt, ok := httpClient.Transport.(*authtransport.RoundTripper); ok {
				authRT = rt
			}
		}
	}

	clientHandler := client.NewHandler(mcpClient)
	switch c.Transport.Type {

	case "stdio":
		stdioOptions := c.Transport.ClientTransportStdio
		if stdioOptions.Command == "" {
			return nil, nil, fmt.Errorf("command is required for stdio transport")
		}
		ret, err := stdio.New(stdioOptions.Command,
			stdio.WithHandler(clientHandler),
			stdio.WithArguments(stdioOptions.Arguments...))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create stdio transport: %w", err)
		}
		return ret, authRT, nil
	case "sse":
		httpOptions := c.Transport.ClientTransportHTTP
		if httpOptions.URL == "" {
			return nil, nil, fmt.Errorf("URL is required for ss transport")
		}
		opts := []sse.Option{}
		if httpClient != nil {
			opts = append(opts, sse.WithHttpClient(httpClient), sse.WithMessageHttpClient(httpClient))
		}
		opts = append(opts, sse.WithHandler(clientHandler))
		ret, err := sse.New(ctx, c.Transport.ClientTransportHTTP.URL, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create SSE transport: %w", err)
		}
		return ret, authRT, nil
	case "streaming":
		httpOptions := c.Transport.ClientTransportHTTP

		opts := []streaming.Option{}
		if httpClient != nil {
			opts = append(opts, streaming.WithHTTPClient(httpClient))
		}
		opts = append(opts, streaming.WithHandler(clientHandler))
		ret, err := streaming.New(ctx, httpOptions.URL, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create streaming transport: %w", err)
		}
		return ret, authRT, nil
	default:
		return nil, authRT, fmt.Errorf("no transport configured")
	}
}

// getOAuthHTTPClient constructs an HTTP client with OAuth2 transport.
// It attempts each OAuth2 config URL in order, returning the first successful client.
func (c *ClientOptions) getOAuthHTTPClient(ctx context.Context) (*http.Client, error) {
	var errs []error
	var memOptions []store.MemoryStoreOption
	for _, raw := range c.Auth.OAuth2ConfigURL { //load oauth client for each config URL, as each mcp server may use different oauth issuer for different resources/tools
		configURL := raw
		if c.Auth.EncryptionKey != "" {
			configURL += "|" + c.Auth.EncryptionKey
		}
		anAuthorizer := authorizer.New()
		oauthCfg := &authorizer.OAuthConfig{ConfigURL: configURL}
		if err := anAuthorizer.EnsureConfig(ctx, oauthCfg); err != nil {
			errs = append(errs, fmt.Errorf("failed to load oauth2 config %q: %w", raw, err))
			continue
		}
		memOptions = append(memOptions, store.WithClientConfig(oauthCfg.Config))
	}
	memStore := store.NewMemoryStore(memOptions...)
	transportOpts := []authtransport.Option{
		authtransport.WithStore(memStore),
		authtransport.WithAuthFlow(flow.NewBrowserFlow()),
	}

	if c.Auth.BackendForFrontend {
		transportOpts = append([]authtransport.Option{authtransport.WithBackendForFrontendAuth()}, transportOpts...)
	}
	if c.Auth.UseIdToken {
		transportOpts = append(transportOpts, authtransport.WithGlobalResource(&authorization.Authorization{
			UseIdToken:                c.Auth.UseIdToken,
			ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{AuthorizationServers: []string{}},
		}))
	}
	rt, err := authtransport.New(transportOpts...)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt}, nil
}

// Options builds client options (metadata and auth interceptor) based on ClientOptions.Auth and Namespace.
func (c *ClientOptions) Options(authRT *authtransport.RoundTripper) []client.Option {
	var result []client.Option
	if c.Namespace != "" {
		result = append(result, client.WithMetadata(map[string]any{"namespace": c.Namespace}))
	}
	if c.ProtocolVersion != "" {
		result = append(result, client.WithProtocolVersion(c.ProtocolVersion))
	}
	if authRT != nil {
		result = append(result, client.WithAuthInterceptor(auth.NewAuthorizer(authRT)))
	}
	return result
}
