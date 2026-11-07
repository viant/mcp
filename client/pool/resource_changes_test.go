package manager

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	authctx "github.com/viant/mcp/client/auth/link"
	primitive "github.com/viant/mcp/client/catalog/index"
)

func TestResourceNotificationsScopeSnapshotsWithoutToolOrConnectionChanges(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithCanonicalUserID(context.Background(), "alice")
	bob := authctx.WithCanonicalUserID(context.Background(), "bob")
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil }
	catalog := func(ctx context.Context, key, server string) *primitive.Catalog[json.RawMessage] {
		partition := m.WithCatalogSources(ctx, m.CatalogPartition(ctx, "verified", "report"), []string{server})
		index, err := m.VisibleCatalogInPartition(ctx, key, partition, time.Now().Add(time.Minute), load)
		require.NoError(t, err)
		return index
	}
	a := catalog(alice, "alice/one", "one")
	b := catalog(bob, "bob/one", "one")
	other := catalog(alice, "alice/two", "two")
	var tool, resource atomic.Int64
	stop := m.OnCatalogChange(func(string) { tool.Add(1) })
	defer stop()
	stop = m.OnResourceCatalogChange(func(string) { resource.Add(1); m.ToolCatalogGeneration("one") })
	defer stop()
	h := &changeHandler{manager: m, server: "one", receiver: m.resourceReceiver(alice)}
	// The transport callback's context lacks identity; the trusted creation scope wins.
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/resources/list_changed"})
	require.Error(t, m.CheckVisibleCatalog(alice, a))
	require.NoError(t, m.CheckVisibleCatalog(bob, b))
	require.NoError(t, m.CheckVisibleCatalog(alice, other))
	require.Zero(t, tool.Load())
	require.EqualValues(t, 1, resource.Load())
	require.Zero(t, m.ToolCatalogGeneration("one"))
	require.Equal(t, [2]uint64{0, 1}, m.ResourceCatalogGeneration(alice, "one"))
	require.Equal(t, [2]uint64{}, m.ResourceCatalogGeneration(bob, "one"))
	// A publicly shared server invalidates its own source for every client.
	h.publicResources = true
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/resources/updated"})
	require.Error(t, m.CheckVisibleCatalog(bob, b))
	require.NoError(t, m.CheckVisibleCatalog(alice, other))
	require.Equal(t, [2]uint64{1, 0}, m.ResourceCatalogGeneration(bob, "one"))
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	require.EqualValues(t, 1, tool.Load())
	require.EqualValues(t, 1, m.ToolCatalogGeneration("one"))
}

func TestCanonicalNotificationsInvalidateAliasesWithinReceiverScope(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithCanonicalUserID(context.Background(), "alice")
	bob := authctx.WithCanonicalUserID(context.Background(), "bob")
	canonical := "mcp://example/service"
	m.pool = map[string]map[string]*entry{
		"alice": {canonical: {resourceReceiver: m.resourceReceiver(alice), aliases: map[string]bool{canonical: true, "direct": true}}},
		"bob":   {canonical: {resourceReceiver: m.resourceReceiver(bob), aliases: map[string]bool{canonical: true, "other-direct": true}}},
	}
	var notified []string
	stop := m.OnResourceCatalogChange(func(name string) { notified = append(notified, name) })
	defer stop()
	m.notifyResourceCatalogChange(canonical, m.resourceReceiver(alice), false)
	require.ElementsMatch(t, []string{canonical, "direct"}, notified)
	require.Equal(t, [2]uint64{0, 1}, m.ResourceCatalogGeneration(alice, "direct"))
	require.Equal(t, [2]uint64{}, m.ResourceCatalogGeneration(bob, "direct"))
	require.Equal(t, [2]uint64{}, m.ResourceCatalogGeneration(alice, "other-direct"))
	m.notifyResourceUpdated(canonical, m.resourceReceiver(alice), false, "window://example/list")
	require.Equal(t, [2]uint64{0, 1}, m.ResourceContentGeneration(alice, "direct"))
	require.Equal(t, [2]uint64{}, m.ResourceContentGeneration(bob, "other-direct"))
	m.notifyCatalogChange(canonical)
	require.EqualValues(t, 1, m.ToolCatalogGeneration(canonical))
	require.EqualValues(t, 1, m.ToolCatalogGeneration("direct"))
}

func TestURIResourceNotificationUsesEntryInvalidationWhileListChangeIsCoarse(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	ctx := authctx.WithCanonicalUserID(context.Background(), "alice")
	h := &changeHandler{manager: mgr, server: "provider", receiver: mgr.resourceReceiver(ctx)}
	var updated []string
	stop := mgr.OnResourceUpdated(func(_, uri string) { updated = append(updated, uri) })
	defer stop()
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/resources/updated", Params: json.RawMessage(`{"uri":"window://team/a"}`)})
	require.Equal(t, []string{"window://team/a"}, updated)
	require.Equal(t, [2]uint64{0, 1}, mgr.ResourceContentGeneration(ctx, "provider"))
	require.Equal(t, [2]uint64{}, mgr.ResourceCatalogGeneration(ctx, "provider"))
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/resources/list_changed"})
	require.Equal(t, [2]uint64{0, 1}, mgr.ResourceCatalogGeneration(ctx, "provider"))
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/resources/updated", Params: json.RawMessage(`{}`)})
	require.Equal(t, [2]uint64{0, 2}, mgr.ResourceCatalogGeneration(ctx, "provider"), "malformed URI event must fall back to coarse invalidation")
}

func TestResourceNotificationFencesBeforePendingInsertionAndDuringPopulation(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	ctx := authctx.WithCanonicalUserID(context.Background(), "alice")
	partition := func() VisibleCatalogPartition {
		return m.WithCatalogSources(ctx, m.CatalogPartition(ctx, "verified", "report"), []string{"one"})
	}
	held := partition()
	m.notifyResourceCatalogChange("one", m.resourceReceiver(ctx), false)
	var loads atomic.Int64
	_, err = m.VisibleCatalogInPartition(ctx, "before-insert", held, time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { loads.Add(1); return nil, nil })
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
	require.Zero(t, loads.Load())
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := m.VisibleCatalogInPartition(ctx, "pending", partition(), time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
			close(started)
			<-release
			return nil, nil
		})
		done <- err
	}()
	<-started
	for i := 0; i < 10; i++ {
		m.notifyResourceCatalogChange("one", m.resourceReceiver(ctx), false)
	}
	close(release)
	require.ErrorIs(t, <-done, primitive.ErrCatalogCursor)
	// A burst does not eagerly reload. The next demand populates once.
	p := partition()
	for i := 0; i < 3; i++ {
		_, err := m.VisibleCatalogInPartition(ctx, "refreshed", p, time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { loads.Add(1); return nil, nil })
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, loads.Load())
	// A later meaningful event after refresh still invalidates immediately.
	m.notifyResourceCatalogChange("one", m.resourceReceiver(ctx), false)
	_, err = m.VisibleCatalogInPartition(ctx, "refreshed", p, time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil })
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}

func TestResourceCatalogScopeNormalizesSourceOrder(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	ctx := authctx.WithCanonicalUserID(context.Background(), "alice")
	left, _, err := m.CatalogScopeForResources(ctx, "", nil, []string{"two", "one"})
	require.NoError(t, err)
	right, _, err := m.CatalogScopeForResources(ctx, "", nil, []string{"one", "two"})
	require.NoError(t, err)
	require.Equal(t, left, right)
	m.notifyResourceCatalogChange("unrelated", m.resourceReceiver(ctx), false)
	unchanged, _, err := m.CatalogScopeForResources(ctx, "", nil, []string{"two", "one"})
	require.NoError(t, err)
	require.Equal(t, left, unchanged)
	m.notifyResourceCatalogChange("one", m.resourceReceiver(ctx), false)
	changed, _, err := m.CatalogScopeForResources(ctx, "", nil, []string{"two", "one"})
	require.NoError(t, err)
	require.NotEqual(t, left, changed)
}
