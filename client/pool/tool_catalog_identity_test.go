package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
)

type identityFixtureOptions struct {
	public, persistent, paginated, large bool
	ttl                                  int
}
type identityFixture struct {
	manager                      *Manager
	provider                     *catalogProvider
	requests, initializes, lists atomic.Int32
}

func newIdentityFixture(t testing.TB, options identityFixtureOptions) *identityFixture {
	t.Helper()
	fixture := &identityFixture{}
	tools := []any{}
	count, fields := 1, 1
	if options.large {
		count, fields = 100, 200
	}
	for i := 0; i < count; i++ {
		properties := map[string]any{}
		for j := 0; j < fields; j++ {
			properties[fmt.Sprint("field-", j)] = map[string]any{"type": "string", "description": "Synthetic schema field for catalog identity benchmark"}
		}
		tools = append(tools, map[string]any{"name": fmt.Sprint("read-", i), "inputSchema": map[string]any{"type": "object", "properties": properties}})
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "identity-session")
		var result any
		switch request.Method {
		case "initialize":
			fixture.initializes.Add(1)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "identity", "version": "1"}}
		case "tools/list":
			fixture.lists.Add(1)
			scope := "private"
			if options.public {
				scope = "public"
			}
			page := map[string]any{"tools": tools, "cacheScope": scope, "ttlMs": options.ttl}
			if options.paginated && request.Params.Cursor == "" {
				page["nextCursor"] = "second"
			}
			result = page
		default:
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(source.Close)
	visibility := "private"
	if options.public {
		visibility = "public"
	}
	fixture.provider = &catalogProvider{cfg: &cfg.MCPClient{ToolsListVisibility: visibility, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: source.URL}}}}}
	var managerOptions []Option
	if options.persistent {
		managerOptions = append(managerOptions, WithPersistentToolCatalog())
	}
	var err error
	fixture.manager, err = New(fixture.provider, managerOptions...)
	require.NoError(t, err)
	t.Cleanup(func() { fixture.manager.CloseConversation("") })
	return fixture
}
func identityUser(subject, token string) context.Context {
	return authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject}), token)
}

func TestToolCatalogIdentityNeverInitializesOrListsColdSource(t *testing.T) {
	f := newIdentityFixture(t, identityFixtureOptions{persistent: true})
	identity, err := f.manager.ToolCatalogIdentity(identityUser("alice", "a"), "", "server")
	require.NoError(t, err)
	require.Empty(t, identity)
	require.Zero(t, f.requests.Load())
	require.Zero(t, f.initializes.Load())
	require.Zero(t, f.lists.Load())
}

func TestToolCatalogIdentityMatchesSnapshotAndPreservesScopeAndInvalidation(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprint("public=", public), func(t *testing.T) {
			f := newIdentityFixture(t, identityFixtureOptions{public: public, persistent: true, paginated: true})
			ctx := identityUser("alice", "")
			if !public {
				ctx = identityUser("alice", "a")
			}
			cli, err := f.manager.Get(ctx, "", "server")
			require.NoError(t, err)
			first, err := cli.ListTools(ctx, nil)
			require.NoError(t, err)
			partial, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
			require.NoError(t, err)
			require.Empty(t, partial, "partial pages cannot identify a complete catalog")
			_, err = cli.ListTools(ctx, first.NextCursor)
			require.NoError(t, err)
			expected, tools, err := f.manager.ToolCatalogSnapshot(ctx, "", "server")
			require.NoError(t, err)
			require.Len(t, tools, 2)
			before := f.requests.Load()
			for i := 0; i < 4; i++ {
				actual, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
				require.NoError(t, err)
				require.Equal(t, expected, actual)
			}
			require.Equal(t, before, f.requests.Load(), "read-only identity must perform no HTTP requests")
			foreign := identityUser("bob", "")
			if !public {
				foreign = identityUser("bob", "b")
			}
			missing, err := f.manager.ToolCatalogIdentity(foreign, "", "server")
			require.NoError(t, err)
			require.Empty(t, missing, "identity never initializes another principal's connection")
			require.Equal(t, before, f.requests.Load())
			_, err = f.manager.Get(foreign, "", "server")
			require.NoError(t, err)
			observed, err := f.manager.ToolCatalogIdentity(foreign, "", "server")
			require.NoError(t, err)
			if public {
				require.Equal(t, expected, observed)
			} else {
				require.Empty(t, observed)
			}
			if !public {
				missing, err = f.manager.ToolCatalogIdentity(identityUser("alice", "rotated"), "", "server")
				require.NoError(t, err)
				require.Empty(t, missing, "changed credential cannot reuse another scope")
			}
			handler := &changeHandler{manager: f.manager, server: "server"}
			handler.OnNotification(ctx, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
			before = f.requests.Load()
			missing, err = f.manager.ToolCatalogIdentity(ctx, "", "server")
			require.NoError(t, err)
			require.Empty(t, missing)
			require.Equal(t, before, f.requests.Load())
			_, err = cli.ListTools(ctx, nil)
			require.NoError(t, err)
			_, err = cli.ListTools(ctx, first.NextCursor)
			require.NoError(t, err)
			refreshed, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
			require.NoError(t, err)
			require.NotEmpty(t, refreshed)
			require.NotEqual(t, expected, refreshed)
			f.provider.cfg.Transport.URL = "https://different.invalid/mcp"
			before = f.requests.Load()
			changed, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
			require.NoError(t, err)
			require.Empty(t, changed)
			require.Equal(t, before, f.requests.Load(), "configuration mismatch cannot initialize its replacement")
		})
	}
}

func TestToolCatalogIdentityMatchesOperationLocalZeroTTLSnapshot(t *testing.T) {
	f := newIdentityFixture(t, identityFixtureOptions{paginated: true})
	ctx := WithToolCatalogReadSnapshot(identityUser("alice", "a"))
	cli, err := f.manager.Get(ctx, "", "server")
	require.NoError(t, err)
	first, err := cli.ListTools(ctx, nil)
	require.NoError(t, err)
	_, err = cli.ListTools(ctx, first.NextCursor)
	require.NoError(t, err)
	expected, _, err := f.manager.ToolCatalogSnapshot(ctx, "", "server")
	require.NoError(t, err)
	actual, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.NotEmpty(t, actual)
	missing, err := f.manager.ToolCatalogIdentity(identityUser("alice", "a"), "", "server")
	require.NoError(t, err)
	require.Empty(t, missing)
	require.EqualValues(t, 2, f.lists.Load())
}

func BenchmarkToolCatalogIdentityLargeHTTP(b *testing.B) {
	f := newIdentityFixture(b, identityFixtureOptions{persistent: true, large: true})
	ctx := identityUser("alice", "a")
	cli, err := f.manager.Get(ctx, "", "server")
	require.NoError(b, err)
	_, err = cli.ListTools(ctx, nil)
	require.NoError(b, err)
	expected, tools, err := f.manager.ToolCatalogSnapshot(ctx, "", "server")
	require.NoError(b, err)
	require.Len(b, tools, 100)
	for _, mode := range []string{"full_snapshot", "identity_only", "selected_schema", "selected_metadata"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var id string
				var err error
				if mode == "full_snapshot" {
					id, _, err = f.manager.ToolCatalogSnapshot(ctx, "", "server")
				} else if mode == "selected_schema" || mode == "selected_metadata" {
					id, _, err = f.manager.ToolCatalogSelect(ctx, "", "server", []string{tools[0].Name}, mode == "selected_metadata")
				} else {
					id, err = f.manager.ToolCatalogIdentity(ctx, "", "server")
				}
				if err != nil || id != expected {
					b.Fatalf("identity mismatch: %v", err)
				}
			}
		})
	}
	require.EqualValues(b, 1, f.lists.Load())
}

func TestToolCatalogIdentityExpiredAndCanceledReadsDoNotRefresh(t *testing.T) {
	f := newIdentityFixture(t, identityFixtureOptions{ttl: 60000})
	ctx := identityUser("alice", "a")
	cli, err := f.manager.Get(ctx, "", "server")
	require.NoError(t, err)
	_, err = cli.ListTools(ctx, nil)
	require.NoError(t, err)
	initial, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
	require.NoError(t, err)
	require.NotEmpty(t, initial)
	f.manager.mu.Lock()
	for _, page := range f.manager.toolCatalog {
		page.expires = time.Now().Add(-time.Second)
	}
	f.manager.mu.Unlock()
	before := f.requests.Load()
	expired, err := f.manager.ToolCatalogIdentity(ctx, "", "server")
	require.NoError(t, err)
	require.Empty(t, expired)
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = f.manager.ToolCatalogIdentity(canceled, "", "server")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, before, f.requests.Load())
}
