package manager

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
)

type persistentCatalogSource struct {
	stubClient
	calls, mutations atomic.Int32
	ttl              int
	scope            schema.ListToolsResultCacheScope
}

func (c *persistentCatalogSource) ListTools(ctx context.Context, _ *string, _ ...client.RequestOption) (*schema.ListToolsResult, error) {
	n := c.calls.Add(1)
	description := authctx.EffectiveUserID(ctx)
	scope := c.scope
	if scope == "" {
		scope = schema.ListToolsResultCacheScopePrivate
	}
	if scope == schema.ListToolsResultCacheScopePublic {
		description = "public"
	}
	return &schema.ListToolsResult{CacheScope: scope, TtlMs: c.ttl, Tools: []schema.Tool{{Name: fmt.Sprint("tool-", n), Description: &description, Meta: map[string]any{"nested": map[string]any{"value": "original"}}}}}, nil
}
func (c *persistentCatalogSource) CallTool(context.Context, *schema.CallToolRequestParams, ...client.RequestOption) (*schema.CallToolResult, error) {
	c.mutations.Add(1)
	return &schema.CallToolResult{}, nil
}

func TestPersistentToolCatalogRetainsPrivatePagesRegardlessOfProducerTTL(t *testing.T) {
	for _, ttl := range []int{0, 1} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			m, _, _, managed, _ := catalogSetup(t, false, WithPersistentToolCatalog())
			source := &persistentCatalogSource{ttl: ttl}
			managed.entry.client = source
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			first, err := managed.ListTools(ctx, nil)
			require.NoError(t, err)
			first.Tools[0].Meta["nested"].(map[string]any)["value"] = "caller mutation"
			time.Sleep(5 * time.Millisecond) // Explicitly pass the producer's positive TTL.
			for i := 0; i < 3; i++ {
				page, err := managed.ListTools(WithToolCatalogReadSnapshot(ctx), nil)
				require.NoError(t, err)
				require.Equal(t, "original", page.Tools[0].Meta["nested"].(map[string]any)["value"])
			}
			require.EqualValues(t, 1, source.calls.Load())
			identity, tools, err := managed.toolCatalogSnapshot(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, identity)
			require.Len(t, tools, 1)
			require.True(t, m.MayHaveToolCatalogAlias("server", "server/tool-1"))
			require.True(t, m.MayHaveToolCatalogAlias("server", "server/absent"), "private absence must remain an unknown candidate")
			// A new scoped insertion runs normal cleanup; the indefinite page survives.
			_, err = managed.ListTools(authctx.WithBearer(ctx, "rotated"), nil)
			require.NoError(t, err)
			same, _, err := managed.toolCatalogSnapshot(ctx)
			require.NoError(t, err)
			require.Equal(t, identity, same)
			_, err = managed.ListTools(ctx, nil)
			require.NoError(t, err)
			require.EqualValues(t, 2, source.calls.Load())
			m.mu.Lock()
			for _, page := range m.toolCatalog {
				require.True(t, page.expires.IsZero())
			}
			m.mu.Unlock()
			for i := 0; i < 2; i++ {
				_, err = managed.CallTool(ctx, &schema.CallToolRequestParams{Name: "mutation"})
				require.NoError(t, err)
			}
			require.EqualValues(t, 2, source.mutations.Load(), "catalog retention must not cache tool invocation results")
		})
	}
}

func TestPersistentToolCatalogPreservesPrincipalCredentialConnectionAndEndpointScopes(t *testing.T) {
	_, provider, _, a, b := catalogSetup(t, false, WithPersistentToolCatalog())
	source := &persistentCatalogSource{}
	a.entry.client = source
	b.entry.client = source
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	for _, read := range []struct {
		ctx     context.Context
		managed *managedClient
	}{{alice, a}, {bob, a}, {alice, b}, {authctx.WithBearer(alice, "new-credential"), a}} {
		first, err := read.managed.ListTools(read.ctx, nil)
		require.NoError(t, err)
		require.Equal(t, authctx.EffectiveUserID(read.ctx), *first.Tools[0].Description)
		again, err := read.managed.ListTools(read.ctx, nil)
		require.NoError(t, err)
		require.Equal(t, first.Tools[0].Name, again.Tools[0].Name)
	}
	require.EqualValues(t, 4, source.calls.Load())
	provider.cfg.Transport.URL = "https://different.example/mcp"
	_, err := a.ListTools(alice, nil)
	require.NoError(t, err)
	require.EqualValues(t, 5, source.calls.Load(), "endpoint configuration must select a different existing cache key")
}

func TestPersistentToolCatalogNotificationRefreshesAndFencesInflightPublication(t *testing.T) {
	m, managed, source, ctx, release := receiptFixture(t)
	require.NoError(t, WithPersistentToolCatalog()(m))
	first := startReceiptRead(ctx, managed)
	<-source.entered
	handler := &changeHandler{manager: m, server: "server"}
	handler.OnNotification(ctx, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	second, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "source-2", second.Tools[0].Name)
	revision, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, revision)
	release()
	require.NoError(t, receiptResult(t, first).err)
	current, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "source-2", current.Tools[0].Name)
	same, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, revision, same, "old inflight source must not replace the notified generation")
	require.EqualValues(t, 2, source.calls.Load())
	handler.OnNotification(ctx, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	empty, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, empty)
	fresh, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "source-3", fresh.Tools[0].Name)
}

func TestPersistentToolCatalogConcurrentReadsShareExistingSingleflight(t *testing.T) {
	m, managed, source, ctx, release := receiptFixture(t)
	require.NoError(t, WithPersistentToolCatalog()(m))
	first := startReceiptRead(ctx, managed)
	<-source.entered
	waiting := []<-chan receiptRead{}
	for i := 0; i < 12; i++ {
		_, done := waitingReceiptContext(t, ctx, managed)
		waiting = append(waiting, done)
	}
	release()
	require.NoError(t, receiptResult(t, first).err)
	for _, done := range waiting {
		got := receiptResult(t, done)
		require.NoError(t, got.err)
		require.Equal(t, "source-1", got.result.Tools[0].Name)
	}
	_, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, source.calls.Load())
}

func TestPersistentToolCatalogDoesNotBypassScope513(t *testing.T) {
	m, _, _, managed, _ := catalogSetup(t, false, WithPersistentToolCatalog())
	source := &persistentCatalogSource{}
	managed.entry.client = source
	for i := 0; i < 513; i++ {
		ctx := authctx.WithBearer(context.Background(), fmt.Sprint("credential-", i))
		_, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	require.Len(t, m.toolCatalog, 513)
	for _, i := range []int{0, 512} {
		_, err := managed.ListTools(authctx.WithBearer(context.Background(), fmt.Sprint("credential-", i)), nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 513, source.calls.Load(), "application-held catalogs must not silently become uncached beyond the legacy cap")
}

func TestPersistentToolCatalogNeverSharesPrivateResponseThroughPublicScope(t *testing.T) {
	m, _, _, a, b := catalogSetup(t, true, WithPersistentToolCatalog())
	source := &persistentCatalogSource{}
	a.entry.client = source
	b.entry.client = source
	_, err := a.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, m.toolCatalog, "an unexpectedly private result cannot publish into public scope")
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	ar, err := a.ListTools(alice, nil)
	require.NoError(t, err)
	br, err := b.ListTools(bob, nil)
	require.NoError(t, err)
	require.Equal(t, "alice", *ar.Tools[0].Description)
	require.Equal(t, "bob", *br.Tools[0].Description)
	_, err = a.ListTools(alice, nil)
	require.NoError(t, err)
	_, err = b.ListTools(bob, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, source.calls.Load())
}

func TestToolCatalogAliasHintsDoNotExcludeAnotherPrincipal(t *testing.T) {
	m, _, _, managed, _ := catalogSetup(t, false, WithPersistentToolCatalog())
	source := &persistentCatalogSource{}
	managed.entry.client = source
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, err := managed.ListTools(alice, nil)
	require.NoError(t, err)
	require.True(t, m.MayHaveToolCatalogAlias("server", "server/tool-2"), "Alice's private inventory cannot exclude Bob's unknown tool")
	b, err := managed.ListTools(bob, nil)
	require.NoError(t, err)
	require.Equal(t, "tool-2", b.Tools[0].Name)
	require.Equal(t, "bob", *b.Tools[0].Description)
}

func TestToolCatalogNegativeAliasHintRequiresCompletePublicPages(t *testing.T) {
	m, _, _, managed, _ := catalogSetup(t, true, WithPersistentToolCatalog())
	first, err := managed.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.True(t, m.MayHaveToolCatalogAlias("server", "server/page2"), "partial public catalog is unknown")
	_, err = managed.ListTools(context.Background(), first.NextCursor)
	require.NoError(t, err)
	require.True(t, m.MayHaveToolCatalogAlias("server", "server/page2"))
	require.False(t, m.MayHaveToolCatalogAlias("server", "server/not-present"))
}

func TestPersistentToolCatalogPublicPagesRemainShareable(t *testing.T) {
	m, _, _, a, b := catalogSetup(t, true, WithPersistentToolCatalog())
	source := &persistentCatalogSource{scope: schema.ListToolsResultCacheScopePublic}
	a.entry.client = source
	b.entry.client = source
	for _, read := range []struct {
		ctx     context.Context
		managed *managedClient
	}{{context.Background(), a}, {authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), b}} {
		got, err := read.managed.ListTools(read.ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "public", *got.Tools[0].Description)
	}
	require.EqualValues(t, 1, source.calls.Load())
	require.False(t, m.MayHaveToolCatalogAlias("server", "server/absent"))
}

func TestPersistentToolCatalogDoesNotRetainFailedSource(t *testing.T) {
	m, managed, source, ctx, release := receiptFixture(t)
	require.NoError(t, WithPersistentToolCatalog()(m))
	source.firstError = true
	first := startReceiptRead(ctx, managed)
	<-source.entered
	release()
	require.Error(t, receiptResult(t, first).err)
	require.Empty(t, m.toolCatalog)
	next, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "source-2", next.Tools[0].Name)
	_, err = managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load())
}
