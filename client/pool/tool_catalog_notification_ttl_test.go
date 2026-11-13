package manager

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp-protocol/schema"
	authctx "github.com/viant/mcp/client/auth/link"
)

func TestPersistentCatalogWithoutNotificationsUsesProducerTTL(t *testing.T) {
	m, _, _, managed, _ := catalogSetup(t, false, WithPersistentToolCatalog())
	source := &persistentCatalogSource{ttl: 60000, noNotifications: true}
	managed.entry.client = source
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	before := time.Now()
	first, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	identity, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, identity)
	for range 100 {
		page, err := managed.ListTools(WithToolCatalogReadSnapshot(ctx), nil)
		require.NoError(t, err)
		require.Equal(t, first.Tools[0].Name, page.Tools[0].Name)
	}
	require.EqualValues(t, 1, source.calls.Load(), "warm discovery must reuse the existing page")
	var expiries []time.Time
	m.mu.Lock()
	for _, page := range m.toolCatalog {
		expiries = append(expiries, page.expires)
		page.expires = time.Now().Add(-time.Second)
	}
	m.mu.Unlock()
	require.NotEmpty(t, expiries)
	for _, expiry := range expiries {
		require.False(t, expiry.Before(before.Add(time.Minute)))
		require.False(t, expiry.After(time.Now().Add(time.Minute)))
	}
	expired, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, expired, "expired metadata must not identify a reusable tool surface")
	require.EqualValues(t, 1, source.calls.Load(), "expiry must not perform eager I/O")
	fresh, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEqual(t, first.Tools[0].Name, fresh.Tools[0].Name)
	require.EqualValues(t, 2, source.calls.Load())
	current, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, current)
	require.NotEqual(t, identity, current)
}

func TestPersistentCatalogWithoutNotificationsHonorsExplicitZeroTTL(t *testing.T) {
	m, _, _, managed, _ := catalogSetup(t, false, WithPersistentToolCatalog())
	source := &persistentCatalogSource{noNotifications: true}
	managed.entry.client = source
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	read := WithToolCatalogReadSnapshot(ctx)
	for range 10 {
		_, err := managed.ListTools(read, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, source.calls.Load(), "one explicit read snapshot remains reusable")
	require.Empty(t, m.toolCatalog, "zero-TTL pages cannot survive independent discovery requests")
	_, err := managed.ListTools(WithToolCatalogReadSnapshot(ctx), nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load())
}

func TestPersistentCatalogDeclinedToolSubscriptionHonorsTTL(t *testing.T) {
	enabled := true
	peer := &subscriptionPeer{
		capabilities: schema.ServerCapabilities{Tools: &schema.ServerCapabilitiesTools{ListChanged: &enabled}, Resources: &schema.ServerCapabilitiesResources{ListChanged: &enabled}},
		accept: func(filter schema.SubscriptionFilter) schema.SubscriptionFilter {
			filter.ToolsListChanged = nil
			return filter
		},
	}
	_, managed, ctx, _ := subscriptionFixture(t, peer, schema.LatestProtocolVersion)
	receiveSubscription(t, peer)
	for range 2 {
		_, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, peer.calls.Load(), "advertisement alone does not establish tool notification delivery")
}

func TestPersistentCatalogNonNotifyingHTTPExpiresWithoutPolling(t *testing.T) {
	fixture := newIdentityFixture(t, identityFixtureOptions{persistent: true, ttl: 100})
	ctx := identityUser("alice", "token")
	client, err := fixture.manager.Get(ctx, "", "server")
	require.NoError(t, err)
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	identity, err := fixture.manager.ToolCatalogIdentity(ctx, "", "server")
	require.NoError(t, err)
	require.NotEmpty(t, identity)
	require.Eventually(t, func() bool {
		current, err := fixture.manager.ToolCatalogIdentity(ctx, "", "server")
		return err == nil && current == ""
	}, 2*time.Second, 5*time.Millisecond)
	require.EqualValues(t, 1, fixture.lists.Load(), "expiry and identity reads perform no source calls")
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, fixture.lists.Load(), "the next discovery performs one refresh")
	fresh, err := fixture.manager.ToolCatalogIdentity(ctx, "", "server")
	require.NoError(t, err)
	require.NotEmpty(t, fresh)
	require.NotEqual(t, identity, fresh)
}
