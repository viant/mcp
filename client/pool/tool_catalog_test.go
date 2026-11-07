package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authcfg "github.com/viant/mcp/client/auth/config"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	mcpcfg "github.com/viant/mcp/client/config"
)

type catalogProvider struct{ cfg *mcpcfg.MCPClient }

func (p *catalogProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return p.cfg, nil
}

type catalogClient struct {
	stubClient
	calls   atomic.Int32
	zeroTTL bool
	onList  func()
}

func (c *catalogClient) ListTools(ctx context.Context, cursor *string, _ ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	c.calls.Add(1)
	if c.onList != nil {
		c.onList()
	}
	name := "generic/tool"
	result := &schema.ListToolsResult{Tools: []schema.Tool{{Name: name, Meta: map[string]interface{}{"custom": map[string]interface{}{"nested": "original"}}}}}
	if c.zeroTTL {
		result.CacheScope = schema.ListToolsResultCacheScopePrivate
		result.TtlMs = 0
	}
	if cursor == nil {
		next := "page2"
		result.NextCursor = &next
	} else {
		result.Tools[0].Name = *cursor
	}
	return result, nil
}

func TestToolCatalogZeroTTLReusesOnlyOneReadSnapshot(t *testing.T) {
	m, provider, client, a, _ := catalogSetup(t, false)
	bff := true
	provider.cfg.Auth = &mcp.ClientAuth{BackendForFrontend: &bff}
	rt, err := authtransport.New()
	require.NoError(t, err)
	m.authRT = rt
	client.zeroTTL = true
	user := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	firstRead := WithToolCatalogReadSnapshot(user)
	first, err := a.ListTools(firstRead, nil)
	require.NoError(t, err)
	first.Tools[0].Meta["custom"].(map[string]interface{})["nested"] = "changed"
	second, err := a.ListTools(firstRead, nil)
	require.NoError(t, err)
	require.Equal(t, "original", second.Tools[0].Meta["custom"].(map[string]interface{})["nested"])
	require.EqualValues(t, 1, client.calls.Load())
	_, err = a.ListTools(WithToolCatalogReadSnapshot(user), nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.calls.Load(), "ttlMs=0 cannot cross independent reads")
	_, err = a.ListTools(authctx.WithBearer(firstRead, "changed-token"), nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, client.calls.Load(), "authorization context must partition a read snapshot")
	m.mu.Lock()
	m.invalidateToolCatalogLocked("server")
	m.mu.Unlock()
	_, err = a.ListTools(firstRead, nil)
	require.NoError(t, err)
	require.EqualValues(t, 4, client.calls.Load(), "tool generation must fence a read snapshot")
}
func catalogSetup(t *testing.T, public bool, options ...Option) (*Manager, *catalogProvider, *catalogClient, *managedClient, *managedClient) {
	t.Helper()
	cfg := &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://example.test/mcp"}}}}
	if public {
		cfg.ToolsListVisibility = "public"
	}
	p := &catalogProvider{cfg: cfg}
	m, err := New(p, options...)
	if err != nil {
		t.Fatal(err)
	}
	client := &catalogClient{}
	// Test the managed boundary without an unknown custom transport factory.
	a := &managedClient{mgr: m, key: "alice:session", serverName: "server", entry: &entry{client: client}}
	b := &managedClient{mgr: m, key: "bob:session", serverName: "server", entry: &entry{client: client}}
	return m, p, client, a, b
}
func TestToolCatalogPublicSharesPagesAndCopiesMetadata(t *testing.T) {
	_, _, client, a, b := catalogSetup(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a
			if i%2 != 0 {
				c = b
			}
			if _, err := c.ListTools(context.Background(), nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n := client.calls.Load(); n != 1 {
		t.Fatalf("shared first page calls=%d", n)
	}
	result, _ := a.ListTools(context.Background(), nil)
	result.Tools[0].Meta["custom"].(map[string]interface{})["nested"] = "mutated"
	other, _ := b.ListTools(context.Background(), nil)
	if other.Tools[0].Meta["custom"].(map[string]interface{})["nested"] != "original" {
		t.Fatal("shared metadata mutated")
	}
	next := *other.NextCursor
	page, err := b.ListTools(context.Background(), &next)
	if err != nil || page.Tools[0].Name != "page2" {
		t.Fatalf("pagination: %v %v", page, err)
	}
	_, _ = a.ListTools(context.Background(), &next)
	if n := client.calls.Load(); n != 2 {
		t.Fatalf("page cache calls=%d", n)
	}
}
func TestToolCatalogPrivateSeparatesSessionsAndAuthContext(t *testing.T) {
	_, _, client, a, b := catalogSetup(t, false)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	_, _ = a.ListTools(alice, nil)
	_, _ = a.ListTools(alice, nil)
	_, _ = b.ListTools(alice, nil)
	_, _ = a.ListTools(authctx.WithBearer(alice, "different"), nil)
	_, _ = a.ListTools(alice, nil, mcpclient.WithAuthToken("override"))
	if n := client.calls.Load(); n != 4 {
		t.Fatalf("private scopes calls=%d", n)
	}
}
func TestToolCatalogConfigNotificationsAndReconnect(t *testing.T) {
	m, p, client, a, _ := catalogSetup(t, true)
	_, _ = a.ListTools(context.Background(), nil)
	p.cfg.Transport.URL = "https://other.test/mcp"
	_, _ = a.ListTools(context.Background(), nil)
	p.cfg.ToolsListVisibility = "private"
	_, _ = a.ListTools(context.Background(), nil)
	h := &changeHandler{manager: m, server: "server"}
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	_, _ = a.ListTools(context.Background(), nil)
	m.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) { return client, nil }
	_, err := m.Reconnect(context.Background(), "session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.ListTools(context.Background(), nil)
	if n := client.calls.Load(); n != 5 {
		t.Fatal(fmt.Sprintf("configuration/generation calls=%d", n))
	}
}
func TestToolCatalogConfiguredAuthNeverSharesPublic(t *testing.T) {
	_, p, client, a, b := catalogSetup(t, true)
	p.cfg.Auth = &mcp.ClientAuth{}
	_, _ = a.ListTools(context.Background(), nil)
	_, _ = b.ListTools(context.Background(), nil)
	if client.calls.Load() != 2 {
		t.Fatal("authenticated catalogs shared")
	}
}

func TestToolCatalogThroughHTTPManager(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "catalog-session")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "catalog", "version": "1"}}
		case "tools/list":
			calls.Add(1)
			result = map[string]any{"tools": []any{map[string]any{"name": "generic/tool", "inputSchema": map[string]any{"type": "object"}}}, "cacheScope": "public", "resultType": "complete", "ttlMs": 60000}
		default:
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	p := &catalogProvider{cfg: &mcpcfg.MCPClient{ToolsListVisibility: "public", ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}}
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	for _, ctx := range []context.Context{alice, bob} {
		cli, err := m.Get(ctx, "same-session", "server")
		if err != nil {
			t.Fatal(err)
		}
		tools, err := cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools.Tools) != 1 {
			t.Fatal("missing catalog")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("public HTTP calls=%d", calls.Load())
	}
	p.cfg.ToolsListVisibility = "private"
	for _, ctx := range []context.Context{alice, bob} {
		cli, err := m.Get(ctx, "same-session", "server")
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("protected HTTP calls=%d", calls.Load())
	}
	cli, err := m.Get(alice, "same-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.ListTools(alice, nil, mcpclient.WithAuthToken("rotated-credential"))
	if err != nil {
		t.Fatal(err)
	}
	sameUserOtherSession, err := m.Get(alice, "another-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sameUserOtherSession.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("rotation/session HTTP calls=%d", calls.Load())
	}

	resolver := &rotatingCatalogResolver{token: "delegated-a"}
	m.credResolver = resolver
	p.cfg.Auth = &mcp.ClientAuth{ExternalResolver: resolver}
	delegated, err := m.Get(alice, "delegated-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 {
		t.Fatalf("delegated cache HTTP calls=%d", calls.Load())
	}
	delegatedIdentity, _, err := m.ToolCatalogSnapshot(alice, "delegated-session", "server")
	if err != nil || delegatedIdentity == "" {
		t.Fatal("missing delegated snapshot", err)
	}
	resolver.token = "delegated-b"
	if identity, _, err := m.ToolCatalogSnapshot(alice, "delegated-session", "server"); err != nil || identity != "" {
		t.Fatal("delegated rotation reused snapshot", err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 7 {
		t.Fatalf("delegated rotation HTTP calls=%d", calls.Load())
	}
	rotatedIdentity, _, err := m.ToolCatalogSnapshot(alice, "delegated-session", "server")
	if err != nil || rotatedIdentity == "" || rotatedIdentity == delegatedIdentity {
		t.Fatal("missing rotated delegated snapshot", err)
	}

}

type rotatingCatalogResolver struct {
	staticCredentialResolver
	token string
}

func (r *rotatingCatalogResolver) Resolve(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: r.token}, nil
}

func TestToolCatalogSnapshotRequiresCompleteCurrentPrincipalPages(t *testing.T) {
	m, p, client, a, b := catalogSetup(t, false)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	first, err := a.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if identity, _, err := a.toolCatalogSnapshot(alice); err != nil || identity != "" {
		t.Fatalf("partial catalog snapshot: %q %v", identity, err)
	}
	_, err = a.ListTools(alice, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !m.MayHaveToolCatalogAlias("server", "server/generic/tool") || !m.MayHaveToolCatalogAlias("server", "absent/tool") {
		t.Fatal("private catalog can suggest aliases but must not exclude another caller")
	}
	identity, tools, err := a.toolCatalogSnapshot(alice)
	if err != nil || identity == "" || len(tools) != 2 {
		t.Fatalf("complete snapshot: %q %v %v", identity, tools, err)
	}
	tools[0].Name = "mutated"
	same, fresh, err := a.toolCatalogSnapshot(alice)
	if err != nil || same != identity || fresh[0].Name == "mutated" {
		t.Fatal("snapshot identity or isolation changed", err)
	}
	for _, check := range []struct {
		c       *managedClient
		ctx     context.Context
		options []mcpclient.RequestOption
	}{
		{b, alice, nil}, {a, authctx.WithBearer(alice, "rotated"), nil}, {a, authctx.WithUserInfo(alice, &authctx.UserInfo{Subject: "bob"}), nil}, {a, alice, []mcpclient.RequestOption{mcpclient.WithAuthToken("override")}},
	} {
		if other, _, err := check.c.toolCatalogSnapshot(check.ctx, check.options...); err != nil || other != "" {
			t.Fatalf("cross-scope snapshot: %q %v", other, err)
		}
	}
	if client.calls.Load() != 2 {
		t.Fatal("snapshot lookup rediscovered tools")
	}
	p.cfg.Transport.URL = "https://changed.test/mcp"
	if other, _, err := a.toolCatalogSnapshot(alice); err != nil || other != "" {
		t.Fatal("configuration reused snapshot", err)
	}
	p.cfg.Transport.URL = "https://example.test/mcp"
	m.mu.Lock()
	for _, page := range m.toolCatalog {
		page.expires = time.Now().Add(-time.Second)
	}
	m.mu.Unlock()
	if other, _, err := a.toolCatalogSnapshot(alice); err != nil || other != "" {
		t.Fatal("expired pages reused snapshot", err)
	}
	_, _ = a.ListTools(alice, nil)
	_, _ = a.ListTools(alice, first.NextCursor)
	renewed, _, err := a.toolCatalogSnapshot(alice)
	if err != nil || renewed == "" || renewed == identity {
		t.Fatal("republication did not change snapshot", err)
	}
	h := &changeHandler{manager: m, server: "server"}
	h.OnNotification(alice, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	if other, _, err := a.toolCatalogSnapshot(alice); err != nil || other != "" {
		t.Fatal("notification reused snapshot", err)
	}
	if !m.MayHaveToolCatalogAlias("server", "server/generic/tool") {
		t.Fatal("missing catalog must remain an unknown candidate")
	}
}

func TestToolCatalogPublicSnapshotSharesAcrossConnections(t *testing.T) {
	_, _, client, a, b := catalogSetup(t, true)
	first, err := a.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.ListTools(context.Background(), first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	identity, _, err := a.toolCatalogSnapshot(context.Background())
	if err != nil || identity == "" {
		t.Fatal("missing public snapshot", err)
	}
	other, _, err := b.toolCatalogSnapshot(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}))
	if err != nil || identity != other {
		t.Fatal("public snapshot not shared", err)
	}
	if client.calls.Load() != 2 {
		t.Fatal("public snapshot rediscovered")
	}
}

func TestHTTPBoundarySharesPrivateToolCatalogOnlyWithinRequest(t *testing.T) {
	m, provider, client, a, _ := catalogSetup(t, false)
	bff := true
	provider.cfg.Auth = &mcp.ClientAuth{BackendForFrontend: &bff}
	rt, err := authtransport.New()
	require.NoError(t, err)
	m.authRT = rt
	client.zeroTTL = true
	user := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	handler := WithRequestCatalog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 3 {
			_, err := a.ListTools(r.Context(), nil)
			require.NoError(t, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for request := 1; request <= 2; request++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metadata", nil).WithContext(user))
		require.Equal(t, http.StatusNoContent, response.Code)
		require.EqualValues(t, request, client.calls.Load(), "a new HTTP request must not inherit a prior private snapshot")
	}
}

func TestToolCatalogCookieJarReusesOnlyMatchingEndpointAndPrincipal(t *testing.T) {
	m, _, client, a, _ := catalogSetup(t, false)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	endpoint, err := url.Parse("https://example.test/mcp")
	require.NoError(t, err)
	m.jarProvider = func(context.Context) (http.CookieJar, error) { return jar, nil }
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "first"}})
	for i := 0; i < 5; i++ {
		_, err = a.ListTools(alice, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, client.calls.Load(), "stable authenticated metadata must not re-discover per tool")
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "second"}})
	_, err = a.ListTools(alice, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.calls.Load(), "changed transport credentials need their own catalog")
	_, err = a.ListTools(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, client.calls.Load(), "private metadata must not cross principals")
}

func TestToolCatalogDoesNotPublishUnderChangedCookieWitness(t *testing.T) {
	m, _, client, a, _ := catalogSetup(t, false)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	endpoint, err := url.Parse("https://example.test/mcp")
	require.NoError(t, err)
	m.cookieJar = jar
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "before"}})
	client.onList = func() { jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "after"}}) }
	_, err = a.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, m.toolCatalog, "a response must not populate a different credential's cache")
	_, err = a.ListTools(context.Background(), nil)
	require.NoError(t, err)
	_, err = a.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.calls.Load())
}

func TestPublicToolCatalogSharesEagerMetadataWithUnusedHostHooks(t *testing.T) {
	m, _, client, a, b := catalogSetup(t, true)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	m.jarProvider = func(context.Context) (http.CookieJar, error) { return jar, nil }
	m.authRTProvider = func(context.Context) *authtransport.RoundTripper {
		t.Fatal("auth:null must not select a host auth hook")
		return nil
	}
	require.True(t, m.IsToolsListPublic(context.Background(), "server"))
	_, err = a.ListTools(context.Background(), nil)
	require.NoError(t, err)
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, err = b.ListTools(bob, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, client.calls.Load(), "startup public metadata must be reusable by authenticated users")
	endpoint, err := url.Parse("https://example.test/mcp")
	require.NoError(t, err)
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "private"}})
	require.False(t, m.IsToolsListPublic(bob, "server"))
	_, err = b.ListTools(bob, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.calls.Load(), "credential-bearing requests must not reuse public metadata")
}

func TestZeroTTLReadSnapshotAvailableToRegistryWithinSameOperation(t *testing.T) {
	m, _, client, managed, _ := catalogSetup(t, false)
	client.zeroTTL = true
	ctx := WithToolCatalogReadSnapshot(context.Background())
	first, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	partial, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, partial, "all pages are required before publishing a catalog identity")
	_, err = managed.ListTools(ctx, first.NextCursor)
	require.NoError(t, err)
	generation, tools, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, generation)
	require.Len(t, tools, 2)
	require.EqualValues(t, 2, client.calls.Load())
	foreign, _, err := managed.toolCatalogSnapshot(authctx.WithBearer(ctx, "other-credential"))
	require.NoError(t, err)
	require.Empty(t, foreign, "an operation snapshot must still isolate credentials")
	outside, _, err := managed.toolCatalogSnapshot(WithToolCatalogReadSnapshot(context.Background()))
	require.NoError(t, err)
	require.Empty(t, outside, "zero-TTL snapshots must not cross operations")
	m.mu.Lock()
	m.invalidateToolCatalogLocked("server")
	m.mu.Unlock()
	invalidated, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, invalidated, "notifications invalidate even an in-operation snapshot")
}

func TestToolCatalogRoutingCookieRotationPreservesOperationSnapshot(t *testing.T) {
	m, provider, client, managed, _ := catalogSetup(t, false)
	provider.cfg.MetadataCacheIgnoredCookies = []string{"affinity"}
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	endpoint, _ := url.Parse("https://example.test/mcp")
	m.cookieJar = jar
	m.authRT, err = authtransport.New(authtransport.WithCookieJar(jar))
	require.NoError(t, err)
	bff := true
	provider.cfg.Auth = &mcp.ClientAuth{BackendForFrontend: &bff}
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "user-one"}, {Name: "affinity", Value: "route-one"}})
	client.zeroTTL = true
	client.onList = func() {
		jar.SetCookies(endpoint, []*http.Cookie{{Name: "affinity", Value: fmt.Sprint(client.calls.Load())}})
	}
	ctx := WithToolCatalogReadSnapshot(context.Background())
	for range 3 {
		_, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, client.calls.Load(), "routing cookie rotation must not discard the source response")
	require.Len(t, jar.Cookies(endpoint), 2, "forwarded cookies remain intact")
	jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "user-two"}})
	_, err = managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.calls.Load(), "authentication cookie rotation still requires fresh discovery")
}

func TestToolCatalogSnapshotDoesNotInitializeMissingConnection(t *testing.T) {
	m, provider, _, _, _ := catalogSetup(t, false)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	provider.cfg.Transport.URL = server.URL
	identity, tools, err := m.ToolCatalogSnapshot(context.Background(), "fresh", "server")
	require.NoError(t, err)
	require.Empty(t, identity)
	require.Empty(t, tools)
	require.Zero(t, requests.Load(), "cache-only reads must never initialize a transport")
}
