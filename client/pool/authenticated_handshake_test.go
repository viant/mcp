package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
	mcpserver "github.com/viant/mcp/server"
)

func TestAuthenticatedHandshakeIncludesUserTokenBeforeSuccessfulAnonymousResponse(t *testing.T) {
	server, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocol.Handler, error) {
		h := protocol.NewDefaultHandler(n, l, c)
		h.Registry.RegisterToolWithSchema("echo", "Echo", schema.ToolInputSchema{Type: "object"}, nil, func(_ context.Context, r *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			return &schema.CallToolResult{StructuredContent: r.Params.Arguments}, nil
		})
		return h, nil
	}))
	require.NoError(t, err)
	server.UseStreamableHTTP(true)
	handler := server.HTTP(context.Background(), "").Handler
	var mu sync.Mutex
	var headers []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer peer.Close()
	// Explicit user-token transport must authenticate the initial handshake.
	var options cfg.MCPClient
	require.NoError(t, json.Unmarshal([]byte(`{"name":"public","auth":{"backendForFrontend":true,"passUserToken":true,"useIdToken":true}}`), &options))
	options.Transport = mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: peer.URL + "/mcp"}}
	options.ProtocolVersion = schema.LatestProtocolVersion
	rt, err := authtransport.New(authtransport.WithTransport(http.DefaultTransport))
	require.NoError(t, err)
	mgr, err := New(&authTokenProviderConfigStub{cfg: &options}, WithAuthRoundTripper(rt))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	ctx, cancel := context.WithTimeout(authctx.WithIDToken(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "fixture-user-token"), 5*time.Second)
	defer cancel()
	client, err := mgr.Get(ctx, "", "public")
	require.NoError(t, err)
	_, err = client.Initialize(ctx)
	require.NoError(t, err)
	ctx = mgr.WithAuthTokenContext(ctx, "public")
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	args := map[string]any{"value": "unchanged"}
	result, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "echo", Arguments: args})
	require.NoError(t, err)
	require.Equal(t, args, result.StructuredContent)
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(headers), 3)
	for _, header := range headers {
		require.Equal(t, "Bearer fixture-user-token", header, "authenticated handshake must not silently negotiate anonymous capabilities")
	}
}

func TestPooledHandshakeFollowsSelectedUserCredential(t *testing.T) {
	var mu sync.Mutex
	var credentials []string
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage
			Method string
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != schema.MethodServerDiscover {
			http.Error(w, "expected server discovery", http.StatusBadRequest)
			return
		}
		credential := r.Header.Get("Authorization")
		mu.Lock()
		credentials = append(credentials, credential)
		mu.Unlock()
		if r.URL.Path == "/concurrent" {
			started <- struct{}{}
			<-release
		}
		extensions := map[string]any{}
		if credential == "Bearer fixture-first-token" {
			extensions[schema.SkillsExtension] = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID,
			"result": map[string]any{
				"supportedVersions": []string{schema.LatestProtocolVersion},
				"capabilities":      map[string]any{"extensions": extensions},
				"cacheScope":        "private",
				"ttlMs":             0,
				"resultType":        "complete",
			},
		})
	}))
	defer peer.Close()

	var options cfg.MCPClient
	require.NoError(t, json.Unmarshal([]byte(`{"name":"fixture","auth":{"backendForFrontend":true,"useIdToken":true}}`), &options))
	options.Transport = mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: peer.URL}}
	options.ProtocolVersion = schema.LatestProtocolVersion
	rt, err := authtransport.New(authtransport.WithTransport(http.DefaultTransport))
	require.NoError(t, err)
	var selections atomic.Int32
	mgr, err := New(&authTokenProviderConfigStub{cfg: &options}, WithAuthRoundTripper(rt), WithUserIDExtractor(authctx.EffectiveUserID), WithTokenContextPreparer(func(ctx context.Context, _ string, _ bool) context.Context {
		selections.Add(1)
		if authctx.IDToken(ctx) == "fixture-unprepared-token" {
			return authctx.WithIDToken(ctx, "fixture-first-token")
		}
		return ctx
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	ctx, cancel := context.WithTimeout(authctx.InjectUser(context.Background(), "fixture-user"), 5*time.Second)
	defer cancel()

	acquire := func(ctx context.Context, wantSkills bool) *managedClient {
		t.Helper()
		client, err := mgr.Get(ctx, "", "fixture")
		require.NoError(t, err)
		managed := client.(*managedClient)
		negotiated, err := managed.NegotiatedServer()
		require.NoError(t, err)
		_, skills := negotiated.Capabilities.Extensions[schema.SkillsExtension]
		require.Equal(t, wantSkills, skills)
		return managed
	}
	public := acquire(ctx, false)
	require.Same(t, public, acquire(ctx, false))
	first := authctx.WithIDToken(ctx, "fixture-unprepared-token")
	authenticated := acquire(first, true)
	require.NotSame(t, public, authenticated)
	require.Same(t, authenticated, acquire(first, true))
	require.Same(t, authenticated, acquire(authctx.WithIDToken(ctx, "fixture-first-token"), true))
	rotated := authctx.WithIDToken(ctx, "fixture-rotated-token")
	updated := acquire(rotated, false)
	require.NotSame(t, authenticated, updated)
	require.Same(t, updated, acquire(rotated, false))
	require.Same(t, public, acquire(ctx, false))
	require.EqualValues(t, 8, selections.Load(), "each acquisition selects its credential once")
	mu.Lock()
	require.Equal(t, []string{"", "Bearer fixture-first-token", "Bearer fixture-rotated-token"}, credentials)
	mu.Unlock()

	options.Transport.URL += "/concurrent"
	results := make(chan struct {
		client mcpclient.Interface
		err    error
	}, 2)
	for _, credential := range []string{"fixture-first-token", "fixture-rotated-token"} {
		go func(credential string) {
			client, err := mgr.Get(authctx.WithIDToken(ctx, credential), "concurrent", "fixture")
			results <- struct {
				client mcpclient.Interface
				err    error
			}{client, err}
		}(credential)
	}
	bothStarted := true
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			bothStarted = false
		}
	}
	close(release)
	firstResult, secondResult := <-results, <-results
	require.True(t, bothStarted, "different credentials must not contend for the same in-flight connection")
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	require.NotSame(t, firstResult.client, secondResult.client)
	require.True(t, mgr.IsCurrentClient(first, "", "fixture", authenticated))
	require.False(t, mgr.EvictCurrentClient(rotated, "", "fixture", authenticated), "another credential cannot retire a held session")
	refreshed, err := mgr.Reconnect(first, "", "fixture")
	require.NoError(t, err)
	require.NotSame(t, authenticated, refreshed)
	require.True(t, mgr.IsCurrentClient(rotated, "", "fixture", updated))
	mgr.CloseConversation("concurrent")
}
