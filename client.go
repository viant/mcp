package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/viant/jsonrpc/transport"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/http/streamable"

	"github.com/viant/jsonrpc/transport/client/stdio"

	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"

	"github.com/viant/mcp/client/auth"
	"github.com/viant/mcp/client/auth/store"
	authtransport "github.com/viant/mcp/client/auth/transport"

	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"

	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp/client"
)

// ClientOptions
//
// defines options for configuring an MCP client.
type ClientOptions struct {
	Name            string          `yaml:"name" json:"name,omitempty"  short:"n" long:"name" description:"mcp name"`
	Version         string          `yaml:"version,omitempty" json:"version,omitempty"  short:"v" long:"version" description:"mcp version"`
	ProtocolVersion string          `yaml:"protocol,omitempty" json:"protocol,omitempty"  short:"p" long:"protocol" description:"mcp protocol"`
	Namespace       string          `yaml:"namespace,omitempty" json:"namespace,omitempty"  short:"N" long:"namespace" description:"mcp namespace"`
	Transport       ClientTransport `yaml:"transport,omitempty" json:"transport,omitempty"  short:"t" long:"transport" description:"mcp transport options"`
	Auth            *ClientAuth     `yaml:"auth,omitempty" json:"auth,omitempty"  short:"a" long:"auth" description:"mcp auth options"`

	// cachedAuthRT and cachedHTTPClient ensure authentication transport and token store
	// are reused across reconnects to avoid losing tokens.
	cachedAuthRT     *authtransport.RoundTripper
	cachedHTTPClient *http.Client

	// CookieJar, if set, is attached to the underlying HTTP client so that
	// servers using cookies (e.g., BFF flows) can persist session cookies
	// across reconnects and calls.
	CookieJar http.CookieJar `yaml:"-" json:"-"`

	// PingIntervalSeconds overrides the default background ping interval
	// used to keep MCP sessions warm and detect transport failures.
	// If <= 0, the default is used (currently 60 seconds).
	PingIntervalSeconds int `yaml:"pingIntervalSeconds,omitempty" json:"pingIntervalSeconds,omitempty"`
}

// ClientAuth defines authentication options for an MCP client.
type ClientAuth struct {
	OAuth2ConfigURL    []string `yaml:"oauth2ConfigURL,omitempty" json:"oauth2ConfigURL,omitempty"  short:"c" long:"config" description:"oauth2 config file"`
	EncryptionKey      string   `yaml:"encryptionKey,omitempty" json:"encryptionKey,omitempty"  short:"k" long:"key" description:"encryption key"`
	UseIdToken         bool     `yaml:"useIdToken,omitempty" json:"useIdToken,omitempty"`
	BackendForFrontend bool     `yaml:"backendForFrontend,omitempty" json:"backendForFrontend,omitempty"  short:"b" long:"backend-for-frontend" description:"use backend for frontend"`

	// Store allows injecting a persistent token store so tokens survive
	// across multiple client instances (e.g., per-user cache in caller).
	Store store.Store `yaml:"-" json:"-"`
}

// ClientTransport defines transport options for an MCP client.
type ClientTransport struct {
	Type                 string `yaml:"type" json:"type"  short:"T" long:"transport-type" description:"mcp transport type, e.g., stdio, sse, streamable" choice:"stdio" choice:"sse" choice:"streamable"`
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
func NewClient(handler pclient.Handler, options *ClientOptions) (*client.Client, error) {
	ctx := context.Background()
	// Build initial transport and capture a factory for future reconnects.
	dial := func(ctx context.Context) (transport.Transport, error) {
		t, _, err := options.getTransport(ctx, handler)
		return t, err
	}

	rpcTransport, authRT, err := options.getTransport(ctx, handler)
	if err != nil {
		return nil, err
	}

	opts := options.Options(authRT)
	opts = append(opts, client.WithClientHandler(handler))
	opts = append(opts, client.WithReconnect(dial))
	// Keepalive ping: use configured interval if provided, else default 60 seconds.
	pingEvery := 60
	if options.PingIntervalSeconds > 0 {
		pingEvery = options.PingIntervalSeconds
	}
	opts = append(opts, client.WithPingInterval(time.Duration(pingEvery)*time.Second))

	cli := client.New(options.Name, options.Version, rpcTransport, opts...)
	if _, err := cli.Initialize(ctx); err != nil {
		return nil, err
	}
	return cli, nil
}

// getTransport constructs a JSON-RPC transport based on ClientOptions.Transport and authentication settings.
func (c *ClientOptions) getTransport(ctx context.Context, handler pclient.Handler) (transport.Transport, *authtransport.RoundTripper, error) {
	var httpClient *http.Client
	var authRT *authtransport.RoundTripper
	if c.Auth != nil {
		if c.Auth.BackendForFrontend {
			// build once and reuse across reconnects
			if c.cachedAuthRT == nil {
				transportOpts := []authtransport.Option{authtransport.WithBackendForFrontendAuth()}
				if c.Auth != nil && c.Auth.Store != nil {
					transportOpts = append(transportOpts, authtransport.WithStore(c.Auth.Store))
				}
				if c.CookieJar != nil {
					transportOpts = append(transportOpts, authtransport.WithCookieJar(c.CookieJar))
				}
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
				c.cachedAuthRT = rt
				// wrap transport with cookie jar if provided
				c.cachedHTTPClient = &http.Client{Transport: rt, Jar: c.CookieJar}
			}
			authRT = c.cachedAuthRT
			httpClient = c.cachedHTTPClient
		} else if len(c.Auth.OAuth2ConfigURL) > 0 {
			var err error
			httpClient, err = c.getOAuthHTTPClient(ctx)
			if err != nil {
				return nil, nil, err
			}
			// We build the HTTP client and keep the original RoundTripper cached,
			// so prefer the cached pointer instead of relying on type assertion on Transport.
			if c.cachedAuthRT != nil {
				authRT = c.cachedAuthRT
			}
		}
	}

	clientHandler := client.NewHandler(handler)
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
	case "streamable":
		httpOptions := c.Transport.ClientTransportHTTP

		opts := []streamable.Option{}
		if httpClient != nil {
			opts = append(opts, streamable.WithHTTPClient(httpClient))
		}
		opts = append(opts, streamable.WithHandler(clientHandler))
		ret, err := streamable.New(ctx, httpOptions.URL, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create streamable transport: %w", err)
		}
		return ret, authRT, nil
	default:
		return nil, authRT, fmt.Errorf("no transport configured")
	}
}

// getOAuthHTTPClient constructs an HTTP client with OAuth2 transport.
// It attempts each OAuth2 config URL in order, returning the first successful client.
func (c *ClientOptions) getOAuthHTTPClient(ctx context.Context) (*http.Client, error) {
	// reuse cached client if present
	if c.cachedHTTPClient != nil {
		return c.cachedHTTPClient, nil
	}

	var errs []error
	var memOptions []store.MemoryStoreOption
	for _, raw := range c.Auth.OAuth2ConfigURL { // load oauth client for each config URL
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
	var authStore store.Store
	if c.Auth != nil && c.Auth.Store != nil {
		authStore = c.Auth.Store
	} else {
		authStore = store.NewMemoryStore(memOptions...)
	}
	transportOpts := []authtransport.Option{
		authtransport.WithStore(authStore),
		authtransport.WithAuthFlow(flow.NewBrowserFlow()),
	}
	if c.CookieJar != nil {
		transportOpts = append(transportOpts, authtransport.WithCookieJar(c.CookieJar))
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
	c.cachedAuthRT = rt
	// wrap transport with cookie jar if provided
	c.cachedHTTPClient = &http.Client{Transport: rt, Jar: c.CookieJar}
	return c.cachedHTTPClient, nil
}

// AuthStore exposes the underlying token store used by the auth transport.
// It allows callers to persist and reuse tokens across client instances.
func (c *ClientOptions) AuthStore() store.Store {
	if c.cachedAuthRT == nil {
		return nil
	}
	return c.cachedAuthRT.Store()
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
