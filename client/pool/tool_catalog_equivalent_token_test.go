package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	client "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
)

func equivalentTokenHTTPFixture(t *testing.T) (*Manager, *atomic.Int32, context.Context) {
	t.Helper()
	calls := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "equivalent-token")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "wire-audit", "version": "1"}}
		case "tools/list":
			calls.Add(1)
			require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
			result = map[string]any{"tools": []any{map[string]any{"name": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "inputSchema": map[string]any{"type": "object"}}}, "cacheScope": "private", "ttlMs": 0}
		default:
			w.WriteHeader(202)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
	}))
	t.Cleanup(upstream.Close)
	provider := &catalogProvider{cfg: &cfg.MCPClient{ToolsListVisibility: cfg.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{Name: "wire-audit", ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: upstream.URL}}, Auth: &mcp.ClientAuth{UseIdToken: true}}}}
	owner, err := New(provider)
	require.NoError(t, err)
	t.Cleanup(func() { owner.CloseConversation(""); owner.CloseConversation("execution") })
	base := authctx.WithIDToken(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "selected-token")
	base = owner.WithAuthTokenContext(base, "source")
	return owner, calls, base
}

func TestHTTPToolCatalogEquivalentExplicitTokenSharesExistingReadSnapshot(t *testing.T) {
	owner, calls, base := equivalentTokenHTTPFixture(t)
	ctx := WithToolCatalogReadSnapshot(base)
	managed, err := owner.Get(ctx, "", "source")
	require.NoError(t, err)
	first, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "selected-token", first.Tools[0].Name)
	same, err := managed.ListTools(ctx, nil, client.WithAuthToken("selected-token"))
	require.NoError(t, err)
	require.Equal(t, "selected-token", same.Tools[0].Name)
	require.EqualValues(t, 1, calls.Load(), "gateway context-token and registry's equal explicit token have identical HTTP credentials")
	override, err := managed.ListTools(ctx, nil, client.WithAuthToken("different-token"))
	require.NoError(t, err)
	require.Equal(t, "different-token", override.Tools[0].Name, "a differing override must still reach the actual transport unchanged")
	require.EqualValues(t, 2, calls.Load())
	again, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "selected-token", again.Tools[0].Name, "override metadata cannot replace the original credential snapshot")
	require.EqualValues(t, 2, calls.Load())
	_, err = managed.ListTools(WithToolCatalogReadSnapshot(base), nil, client.WithAuthToken("selected-token"))
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load(), "zero TTL still requires a source read for the next independent operation")
}

func TestHTTPToolCatalogEquivalentTokenRetainsAuthorityAndSessionPartitions(t *testing.T) {
	owner, calls, base := equivalentTokenHTTPFixture(t)
	ctx := WithToolCatalogReadSnapshot(base)
	managed, err := owner.Get(ctx, "", "source")
	require.NoError(t, err)
	contexts := []context.Context{ctx, authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"}), authctx.WithProvider(ctx, "another-provider"), authctx.WithBearer(ctx, "another-bearer"), authctx.WithIDToken(ctx, "another-context-id-token")}
	for index, scope := range contexts {
		result, err := managed.ListTools(scope, nil, client.WithAuthToken("selected-token"))
		require.NoError(t, err)
		require.Equal(t, "selected-token", result.Tools[0].Name, "credential normalization must not change wire forwarding")
		require.EqualValues(t, index+1, calls.Load(), "principal/provider/context credentials remain separate even with equal wire overrides")
	}
	other, err := owner.Get(ctx, "execution", "source")
	require.NoError(t, err)
	_, err = other.ListTools(ctx, nil, client.WithAuthToken("selected-token"))
	require.NoError(t, err)
	require.EqualValues(t, len(contexts)+1, calls.Load(), "a separate conversation transport must not inherit another entry's snapshot")
}

func TestEqualExplicitTokenDoesNotMergeUnknownOrNonHTTPTransports(t *testing.T) {
	for _, mode := range []string{"stdio", "unknown", "custom-http"} {
		t.Run(mode, func(t *testing.T) {
			owner, provider, source, managed, _ := catalogSetup(t, false)
			source.zeroTTL = true
			switch mode {
			case "stdio":
				provider.cfg.Transport.Type = "stdio"
				provider.cfg.Transport.URL = ""
			case "unknown":
				provider.cfg.Transport.Type = "custom"
			case "custom-http":
				owner.newClientFn = func(context.Context, string, string) (client.Interface, error) { return source, nil }
			}
			base := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			ctx := WithToolCatalogReadSnapshot(context.WithValue(base, authtransport.ContextAuthTokenKey, "same-token"))
			_, err := managed.ListTools(ctx, nil)
			require.NoError(t, err)
			_, err = managed.ListTools(ctx, nil, client.WithAuthToken("same-token"))
			require.NoError(t, err)
			require.EqualValues(t, 2, source.calls.Load(), "unknown and stdio transports may distinguish explicit request metadata from a context token")
		})
	}
}
