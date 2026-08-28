package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/http/streamable"

	"github.com/viant/jsonrpc/transport/client/stdio"

	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"

	"github.com/viant/mcp/client/auth"
	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/store"
	authtransport "github.com/viant/mcp/client/auth/transport"

	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp-protocol/schema"

	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp/client"
)

const (
	compatibilityProtocolVersionJune2025 = "2025-06-18"
	invalidMCPProtocolVersionMarker      = "invalid MCP-Protocol-Version"
	protocolRejectionScanLimit           = 64 << 10
	protocolRejectionScanTimeout         = 250 * time.Millisecond
)

var (
	errInvalidMCPProtocolVersion = errors.New(invalidMCPProtocolVersionMarker)
	errProtocolRejectionScan     = errors.New("timed out scanning SSE protocol rejection response")
)

type contextAuthHeaderTransport struct {
	base http.RoundTripper
}

type sseProtocolRejectionTransport struct {
	base http.RoundTripper
}

type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

func (t *contextAuthHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header.Get(schema.HeaderProtocolVersion) == schema.LatestProtocolVersion && clone.Body != nil {
		body, err := io.ReadAll(clone.Body)
		if err != nil {
			return nil, fmt.Errorf("read MCP request body: %w", err)
		}
		_ = clone.Body.Close()
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		if req.GetBody != nil {
			clone.GetBody = req.GetBody
		} else {
			clone.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		}
		if err := applyMCPStandardHeaders(clone.Header, body); err != nil {
			return nil, err
		}
	}
	token, _ := req.Context().Value(authtransport.ContextAuthTokenKey).(string)
	if token != "" && clone.Header.Get("Authorization") == "" {
		clone.Header.Set("Authorization", "Bearer "+token)
	}
	return base.RoundTrip(clone)
}

func (t *sseProtocolRejectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(req)
	if err != nil || response == nil || response.Body == nil ||
		req.Method != http.MethodGet || response.StatusCode != http.StatusBadRequest {
		return response, err
	}

	bodyPrefix, readErr := readSSEProtocolRejectionPrefix(req.Context(), response.Body)
	if readErr != nil {
		_ = response.Body.Close()
		return nil, readErr
	}
	response.Body = &prefixedReadCloser{
		Reader: io.MultiReader(bytes.NewReader(bodyPrefix), response.Body),
		Closer: response.Body,
	}
	if !bytes.Contains(bodyPrefix, []byte(invalidMCPProtocolVersionMarker)) {
		return response, nil
	}
	_ = response.Body.Close()
	return nil, errInvalidMCPProtocolVersion
}

func readSSEProtocolRejectionPrefix(ctx context.Context, body io.ReadCloser) ([]byte, error) {
	stopContextClose := context.AfterFunc(ctx, func() {
		_ = body.Close()
	})
	timeoutClose := time.AfterFunc(protocolRejectionScanTimeout, func() {
		_ = body.Close()
	})

	data, err := io.ReadAll(io.LimitReader(body, protocolRejectionScanLimit))
	timedOut := !timeoutClose.Stop()
	stopContextClose()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if timedOut {
		return nil, errProtocolRejectionScan
	}
	return data, err
}

func applyMCPStandardHeaders(header http.Header, body []byte) error {
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode MCP request for standard headers: %w", err)
	}
	if request.Method == "" {
		// JSON-RPC responses to server-initiated requests use the same stateless
		// POST transport but do not carry request-routing headers.
		return nil
	}
	// Notifications do not carry the standard request routing headers.
	if len(request.ID) == 0 || string(request.ID) == "null" {
		return nil
	}
	header.Set(schema.HeaderMethod, request.Method)
	field := standardHeaderNameField(request.Method)
	if field == "" {
		return nil
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return fmt.Errorf("decode %s params for %s: %w", request.Method, schema.HeaderName, err)
	}
	var name string
	if value := params[field]; len(value) > 0 {
		if err := json.Unmarshal(value, &name); err != nil {
			return fmt.Errorf("decode %s params.%s: %w", request.Method, field, err)
		}
	}
	if name == "" {
		return fmt.Errorf("%s params.%s is required", request.Method, field)
	}
	header.Set(schema.HeaderName, name)
	return nil
}

func standardHeaderNameField(method string) string {
	switch method {
	case schema.MethodToolsCall, schema.MethodPromptsGet:
		return "name"
	case schema.MethodResourcesRead:
		return "uri"
	default:
		return ""
	}
}

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
	OAuth2ConfigURL []string `yaml:"oauth2ConfigURL,omitempty" json:"oauth2ConfigURL,omitempty"  short:"c" long:"config" description:"oauth2 config file"`
	EncryptionKey   string   `yaml:"encryptionKey,omitempty" json:"encryptionKey,omitempty"  short:"k" long:"key" description:"encryption key"`
	UseIdToken      bool     `yaml:"useIdToken,omitempty" json:"useIdToken,omitempty"`
	// BackendForFrontend enables BFF auth mode. When nil (not configured),
	// defaults to true if an auth RoundTripper is injected via SetAuthTransport.
	// Set explicitly to false to disable BFF and use standard OAuth2 flow.
	BackendForFrontend *bool `yaml:"backendForFrontend,omitempty" json:"backendForFrontend,omitempty"  short:"b" long:"backend-for-frontend" description:"use backend for frontend"`

	// PassUserToken controls whether the logged-in user's auth token is
	// forwarded to this MCP server. When nil (not configured), defaults to
	// true — the app user's token is sent as Bearer on the first probe so
	// MCP servers sharing the same IDP can authenticate without a separate
	// OAuth flow. Set explicitly to false to disable token forwarding.
	//
	// PassUserToken is a legacy forwarding flag: it is meaningful only for
	// the built-in (non-delegated) auth paths. In delegated mode (Mode
	// "oauth" with ProviderRef/InlineProvider, or an installed
	// ExternalResolver) it is rejected when true and ShouldPassUserToken
	// always reports false — validated host-token reuse is requested through
	// WorkspaceTokenReuse and implemented by the external resolver, which
	// owns token provenance.
	PassUserToken *bool `yaml:"passUserToken,omitempty" json:"passUserToken,omitempty" description:"forward logged-in user token to MCP server"`

	// Store allows injecting a persistent token store so tokens survive
	// across multiple client instances (e.g., per-user cache in caller).
	Store store.Store `yaml:"-" json:"-"`

	// Mode selects the auth mode. Empty preserves legacy behaviour
	// (OAuth2ConfigURL/BFF); "oauth" enables delegated multi-provider OAuth
	// driven by an external CredentialResolver.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty" description:"auth mode: empty (legacy) or oauth (delegated)"`

	// ProviderRef references an OAuth provider registered with the host's
	// ProviderRegistry. Mutually exclusive with InlineProvider.
	ProviderRef string `yaml:"providerRef,omitempty" json:"providerRef,omitempty" description:"registered oauth provider reference"`

	// ClientRef selects a client registration within the referenced or
	// inline provider; empty resolves the provider default client.
	ClientRef string `yaml:"clientRef,omitempty" json:"clientRef,omitempty" description:"oauth client reference within the provider"`

	// InlineProvider defines the OAuth provider inline instead of by
	// reference. Mutually exclusive with ProviderRef.
	InlineProvider *authcfg.OAuthProvider `yaml:"inlineProvider,omitempty" json:"inlineProvider,omitempty"`

	// Resource is the protected resource (audience) the credential must
	// target; defaults to the MCP transport URL when empty.
	Resource string `yaml:"resource,omitempty" json:"resource,omitempty" description:"protected resource / audience"`

	// Scopes lists the OAuth scopes required for this MCP server.
	Scopes []string `yaml:"scopes,omitempty" json:"scopes,omitempty" description:"required oauth scopes"`

	// TokenType selects accessToken (default) or idToken.
	TokenType string `yaml:"tokenType,omitempty" json:"tokenType,omitempty" description:"accessToken or idToken"`

	// Resolution selects eager (default: resolve before initialize/discovery)
	// or challenge (resolve only after a 401).
	Resolution string `yaml:"resolution,omitempty" json:"resolution,omitempty" description:"eager or challenge"`

	// WorkspaceTokenReuse tells the external resolver whether a host token
	// may satisfy this requirement: never (default) or ifCompatible.
	WorkspaceTokenReuse string `yaml:"workspaceTokenReuse,omitempty" json:"workspaceTokenReuse,omitempty" description:"never or ifCompatible"`

	// AllowCrossOriginResource permits Resource to have a different origin
	// than the MCP transport URL (host-side allowlisting decision).
	AllowCrossOriginResource bool `yaml:"allowCrossOriginResource,omitempty" json:"allowCrossOriginResource,omitempty"`

	// ExternalResolver, when set, delegates credential acquisition, refresh
	// and invalidation to the host. viant/mcp then disables its legacy
	// interactive/browser fallback for this client, attaches only the
	// resolved credential, and coordinates 401 recovery: in eager mode
	// (default Resolution) it resolves proactively before
	// initialize/discovery/every request and performs at most one refresh
	// plus one retry after 401; in challenge mode (Resolution "challenge")
	// the first request carries no Authorization and the resolver is
	// consulted only after a 401, with at most one authenticated retry.
	// Afterwards a typed *config.OAuthLinkRequiredError is returned;
	// Invalidate fires only after a resolved/refreshed credential is
	// terminally rejected. The resolved value is never installed into any
	// host identity context.
	ExternalResolver authcfg.CredentialResolver `yaml:"-" json:"-"`

	// ProviderRegistry optionally resolves ProviderRef during requirement
	// compilation so the issuer is known before the first request.
	ProviderRegistry authcfg.ProviderRegistry `yaml:"-" json:"-"`
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

func boolPtr(v bool) *bool { return &v }

// ShouldPassUserToken reports whether the logged-in user's token should be
// forwarded to this MCP server. Defaults to true when not configured.
// Delegated mode always reports false: only the credential returned by the
// external resolver may reach a delegated MCP server, and host-token reuse is
// decided by the resolver through the WorkspaceTokenReuse policy.
func (c *ClientAuth) ShouldPassUserToken() bool {
	if c == nil {
		return true
	}
	if c.IsDelegated() {
		return false
	}
	if c.PassUserToken == nil {
		return true
	}
	return *c.PassUserToken
}

func (c *ClientOptions) Init() {
	if c.Name == "" {
		c.Name = "MCPClient"
		c.Version = "0.1"
	}
	if c.ProtocolVersion == "" {
		c.ProtocolVersion = schema.LatestProtocolVersion
	}
}

// SetAuthTransport injects a pre-built auth RoundTripper and HTTP client
// into the options so that getTransport reuses them instead of building new
// ones. This is the safe way for managers to inject per-user auth without
// leaking the internal cachedAuthRT/cachedHTTPClient fields.
func (c *ClientOptions) SetAuthTransport(rt *authtransport.RoundTripper, httpClient *http.Client) {
	if c == nil || rt == nil {
		return
	}
	c.cachedAuthRT = rt
	c.cachedHTTPClient = httpClient
	// Default to BFF mode when not explicitly configured — the standard
	// pattern for web UI contexts. Respects explicit false.
	if c.Auth == nil {
		c.Auth = &ClientAuth{BackendForFrontend: boolPtr(true)}
	} else if c.Auth.BackendForFrontend == nil && len(c.Auth.OAuth2ConfigURL) == 0 {
		c.Auth.BackendForFrontend = boolPtr(true)
	}
}

// NewClient creates an MCP client with transport and authorization configured via ClientOptions.
func NewClient(handler pclient.Handler, options *ClientOptions) (*client.Client, error) {
	return NewClientWithContext(context.Background(), handler, options)
}

// NewClientWithContext creates an MCP client and runs discovery (or the legacy
// initialize handshake) with the supplied context so per-request auth tokens
// and other caller state are available during negotiation.
func NewClientWithContext(ctx context.Context, handler pclient.Handler, options *ClientOptions) (*client.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	autoProtocol := strings.TrimSpace(options.ProtocolVersion) == ""
	options.Init()
	initCtx := ctx
	cancelProbe := func() {}
	if autoProtocol && options.ProtocolVersion == schema.LatestProtocolVersion {
		initCtx, cancelProbe = context.WithTimeout(ctx, 5*time.Second)
	}
	cli, err := newClientWithProtocol(ctx, initCtx, handler, options)
	cancelProbe()
	if err == nil {
		return cli, nil
	}
	if cli != nil {
		cli.Close()
	}
	if !autoProtocol || options.ProtocolVersion != schema.LatestProtocolVersion {
		return nil, err
	}
	latestErr := err
	options.ProtocolVersion = schema.LegacyProtocolVersion
	legacyClient, legacyErr := newClientWithProtocol(ctx, ctx, handler, options)
	if legacyErr == nil {
		return legacyClient, nil
	}
	if legacyClient != nil {
		legacyClient.Close()
	}
	if !isProtocolVersionNegotiationError(legacyErr) {
		return nil, fmt.Errorf("automatic MCP protocol attempts failed: %s discovery: %v; %s initialize: %w",
			schema.LatestProtocolVersion, latestErr, schema.LegacyProtocolVersion, legacyErr)
	}

	options.ProtocolVersion = compatibilityProtocolVersionJune2025
	compatibilityClient, compatibilityErr := newClientWithProtocol(ctx, ctx, handler, options)
	if compatibilityErr == nil {
		return compatibilityClient, nil
	}
	if compatibilityClient != nil {
		compatibilityClient.Close()
	}
	return nil, fmt.Errorf("automatic MCP protocol attempts failed: %s discovery: %v; %s initialize: %v; %s initialize: %w",
		schema.LatestProtocolVersion, latestErr,
		schema.LegacyProtocolVersion, legacyErr,
		compatibilityProtocolVersionJune2025, compatibilityErr)
}

func isProtocolVersionNegotiationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errInvalidMCPProtocolVersion) {
		return true
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == schema.ErrorCodeUnsupportedProtocolVersion {
		return true
	}

	message := strings.ToLower(err.Error())
	if !strings.Contains(message, strings.ToLower(invalidMCPProtocolVersionMarker)) {
		return false
	}
	status, ok := transportHTTPStatus(message)
	return ok && status == http.StatusBadRequest
}

func transportHTTPStatus(message string) (int, bool) {
	const marker = "invalid status code:"
	index := strings.Index(message, marker)
	if index == -1 {
		return 0, false
	}
	fields := strings.Fields(message[index+len(marker):])
	if len(fields) == 0 {
		return 0, false
	}
	status, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	return status, err == nil
}

func newClientWithProtocol(ctx, initCtx context.Context, handler pclient.Handler, options *ClientOptions) (*client.Client, error) {
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
	if _, err := cli.Initialize(initCtx); err != nil {
		return cli, err
	}
	return cli, nil
}

// getTransport constructs a JSON-RPC transport based on ClientOptions.Transport and authentication settings.
func (c *ClientOptions) getTransport(ctx context.Context, handler pclient.Handler) (transport.Transport, *authtransport.RoundTripper, error) {
	var httpClient *http.Client
	var authRT *authtransport.RoundTripper
	if err := c.Auth.Validate(); err != nil {
		return nil, nil, err
	}
	// If a pre-built auth transport was injected via SetAuthTransport, reuse it
	// regardless of which Auth branch applies (BFF, OAuth2, or none).
	if c.cachedAuthRT != nil && c.cachedHTTPClient != nil {
		if c.Auth != nil && c.Auth.ExternalResolver != nil && !c.cachedAuthRT.HasCredentialResolver() {
			return nil, nil, fmt.Errorf("auth: external credential resolver and injected auth transport are mutually exclusive")
		}
		authRT = c.cachedAuthRT
		httpClient = c.cachedHTTPClient
	} else if c.Auth != nil && c.Auth.ExternalResolver != nil {
		// Delegated OAuth: the external resolver owns credential policy and
		// this transport owns attachment plus single-refresh/single-retry 401
		// recovery. Build once and reuse across reconnects.
		requirement, err := c.Auth.CompileRequirement(ctx, c.Name, c.Transport.URL)
		if err != nil {
			return nil, nil, err
		}
		transportOpts := []authtransport.Option{
			authtransport.WithCredentialResolver(c.Auth.ExternalResolver, requirement),
		}
		if c.CookieJar != nil {
			transportOpts = append(transportOpts, authtransport.WithCookieJar(c.CookieJar))
		}
		rt, err := authtransport.New(transportOpts...)
		if err != nil {
			return nil, nil, err
		}
		c.cachedAuthRT = rt
		c.cachedHTTPClient = &http.Client{Transport: rt, Jar: c.CookieJar}
		authRT = c.cachedAuthRT
		httpClient = c.cachedHTTPClient
	} else if c.Auth != nil && c.Auth.IsDelegated() {
		return nil, nil, fmt.Errorf("auth: mode %q with providerRef/inlineProvider requires an external credential resolver", c.Auth.Mode)
	} else if c.Auth != nil {
		if c.Auth.BackendForFrontend != nil && *c.Auth.BackendForFrontend {
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
	if httpClient == nil && (c.CookieJar != nil || c.Transport.Type == "sse" || c.Transport.Type == "streamable") {
		httpClient = &http.Client{Jar: c.CookieJar}
	}
	if httpClient != nil {
		httpClient = wrapContextAuthHTTPClient(httpClient)
		if c.Transport.Type == "sse" {
			httpClient = wrapSSEProtocolRejectionHTTPClient(httpClient)
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
		if c.ProtocolVersion != "" {
			opts = append(opts, sse.WithProtocolVersion(c.ProtocolVersion))
		}
		if httpClient != nil {
			opts = append(opts, sse.WithHttpClient(httpClient), sse.WithMessageHttpClient(httpClient))
		}
		opts = append(opts, sse.WithHandler(clientHandler))
		ret, err := sse.New(ctx, c.Transport.ClientTransportHTTP.URL, opts...)
		if err != nil {
			if ret != nil {
				_ = ret.Close()
			}
			return nil, nil, fmt.Errorf("failed to create SSE transport: %w", err)
		}
		return ret, authRT, nil
	case "streamable":
		httpOptions := c.Transport.ClientTransportHTTP

		opts := []streamable.Option{}
		if c.ProtocolVersion != "" {
			opts = append(opts, streamable.WithProtocolVersion(c.ProtocolVersion))
		}
		if c.ProtocolVersion == schema.LatestProtocolVersion {
			opts = append(opts,
				streamable.WithStateless(),
				streamable.WithRunTimeout(0),
				streamable.WithRequestHeaderProvider(func(_ context.Context, body []byte, header http.Header) error {
					return applyMCPStandardHeaders(header, body)
				}),
			)
		}
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

func wrapContextAuthHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	clone.Transport = &contextAuthHeaderTransport{base: client.Transport}
	return &clone
}

func wrapSSEProtocolRejectionHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	clone.Transport = &sseProtocolRejectionTransport{base: client.Transport}
	return &clone
}

// getOAuthHTTPClient constructs an HTTP client with OAuth2 transport.
// It attempts each OAuth2 config URL in order, returning the first successful client.
func (c *ClientOptions) getOAuthHTTPClient(ctx context.Context) (*http.Client, error) {
	// reuse cached client if present
	if c.cachedHTTPClient != nil {
		return c.cachedHTTPClient, nil
	}

	var errs []error
	var clientConfigs []*oauth2.Config
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
		clientConfigs = append(clientConfigs, oauthCfg.Config)
	}
	var authStore store.Store
	if c.Auth != nil && c.Auth.Store != nil {
		// Retain loaded OAuth client configs in the injected store so they
		// are not discarded when a caller supplies its own persistence.
		authStore = c.Auth.Store
		for _, clientConfig := range clientConfigs {
			if err := store.InstallClientConfig(authStore, clientConfig); err != nil {
				return nil, fmt.Errorf("failed to install oauth2 client config into injected store: %w", err)
			}
		}
	} else {
		var memOptions []store.MemoryStoreOption
		for _, clientConfig := range clientConfigs {
			memOptions = append(memOptions, store.WithClientConfig(clientConfig))
		}
		authStore = store.NewMemoryStore(memOptions...)
	}
	transportOpts := []authtransport.Option{
		authtransport.WithStore(authStore),
		authtransport.WithAuthFlow(flow.NewBrowserFlow()),
	}
	if c.CookieJar != nil {
		transportOpts = append(transportOpts, authtransport.WithCookieJar(c.CookieJar))
	}
	if c.Auth.BackendForFrontend != nil && *c.Auth.BackendForFrontend {
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
	// The legacy JSON-RPC auth interceptor can start interactive OAuth flows;
	// in delegated mode the transport-level coordinator is the sole owner of
	// credential attachment and 401 recovery, so the interceptor is skipped.
	if authRT != nil && !authRT.HasCredentialResolver() {
		result = append(result, client.WithAuthInterceptor(auth.NewAuthorizer(authRT)))
	}
	return result
}
