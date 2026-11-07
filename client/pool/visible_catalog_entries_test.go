package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/mcp/client/auth/link"
	primitive "github.com/viant/mcp/client/catalog/index"
)

func TestVisibleCatalogKeyedEntriesKeep450ResourcesInOneNamespaceSlot(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	partition := mgr.WithCatalogSources(context.Background(), mgr.PublicCatalogPartition("window"), []string{"provider"})
	calls := 0
	get := func(name string) (json.RawMessage, error) {
		return mgr.VisibleCatalogEntryInPartition(context.Background(), "provider/workspace", partition, name, func(context.Context) (json.RawMessage, time.Time, error) {
			calls++
			return json.RawMessage(`{"original":true}`), time.Time{}, nil
		})
	}
	for i := 0; i < 450; i++ {
		_, err = get(fmt.Sprintf("window://workspace/%03d", i))
		require.NoError(t, err)
	}
	for i := 449; i >= 0; i-- {
		raw, err := get(fmt.Sprintf("window://workspace/%03d", i))
		require.NoError(t, err)
		raw[0] = 'X'
	}
	require.Equal(t, 450, calls)
	mgr.mu.Lock()
	require.Len(t, mgr.visibleCatalogs, 1)
	slot := mgr.visibleCatalogs["provider/workspace"]
	require.Len(t, slot.entries, 450)
	require.Equal(t, "window://workspace/000", slot.entries[0].key)
	mgr.mu.Unlock()
	raw, err := get("window://workspace/000")
	require.NoError(t, err)
	require.JSONEq(t, `{"original":true}`, string(raw))
	mgr.notifyResourceCatalogChange("provider", [32]byte{}, true)
	// Old captured generations cannot publish after notification.
	_, err = get("window://workspace/000")
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
	partition = mgr.WithCatalogSources(context.Background(), mgr.PublicCatalogPartition("window"), []string{"provider"})
	_, err = get("window://workspace/000")
	require.NoError(t, err)
	require.Equal(t, 451, calls)
}

func TestVisibleCatalogKeyedEntryUsesExistingSingleflightAndLateInvalidation(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	partition := mgr.WithCatalogSources(context.Background(), mgr.PublicCatalogPartition("window"), []string{"provider"})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return json.RawMessage(`{"value":"original"}`), time.Time{}, nil
	}
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := mgr.VisibleCatalogEntryInPartition(context.Background(), "scope", partition, "uri", load)
			results <- err
		}()
	}
	<-started
	close(release)
	wait.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	partition = mgr.WithCatalogSources(context.Background(), mgr.PublicCatalogPartition("window"), []string{"other"})
	_, err = mgr.VisibleCatalogEntryInPartition(context.Background(), "other-scope", partition, "uri", func(context.Context) (json.RawMessage, time.Time, error) {
		mgr.notifyResourceCatalogChange("other", [32]byte{}, true)
		return json.RawMessage(`{}`), time.Time{}, nil
	})
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}

func TestResourceUpdatedEvictsOnlyNamedDefinitionAndFencesPendingLoad(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	partition := mgr.WithCatalogSources(ctx, mgr.PublicCatalogPartition("window"), []string{"provider"})
	uriA, uriB := "window://team/a", "window://team/b"
	loads := map[string]int{}
	get := func(uri string) (json.RawMessage, error) {
		return mgr.VisibleCatalogEntryInPartition(ctx, "provider/windows", partition, uri+"\x00working", func(context.Context) (json.RawMessage, time.Time, error) {
			loads[uri]++
			raw, err := json.Marshal(map[string]int{"load": loads[uri]})
			return raw, time.Time{}, err
		})
	}
	_, err = get(uriA)
	require.NoError(t, err)
	_, err = get(uriB)
	require.NoError(t, err)
	var updated []string
	stop := mgr.OnResourceUpdated(func(server, uri string) { updated = append(updated, server+" "+uri) })
	defer stop()
	mgr.notifyResourceUpdated("provider", [32]byte{}, true, uriA)
	require.Equal(t, []string{"provider " + uriA}, updated)
	require.Equal(t, [2]uint64{}, mgr.ResourceCatalogGeneration(ctx, "provider"), "content update must retain the list snapshot")
	require.Equal(t, [2]uint64{1, 0}, mgr.ResourceContentGeneration(ctx, "provider"))
	_, err = get(uriB)
	require.NoError(t, err)
	require.Equal(t, 1, loads[uriB])
	raw, err := get(uriA)
	require.NoError(t, err)
	require.JSONEq(t, `{"load":2}`, string(raw))

	started, release := make(chan struct{}), make(chan struct{})
	uriPending := "window://team/pending"
	done := make(chan error, 1)
	go func() {
		_, loadErr := mgr.VisibleCatalogEntryInPartition(ctx, "provider/windows", partition, uriPending+"\x00working", func(context.Context) (json.RawMessage, time.Time, error) {
			close(started)
			<-release
			return json.RawMessage(`{"stale":true}`), time.Time{}, nil
		})
		done <- loadErr
	}()
	<-started
	mgr.notifyResourceUpdated("provider", [32]byte{}, true, uriPending)
	close(release)
	require.ErrorIs(t, <-done, primitive.ErrCatalogCursor)
	_, err = get(uriB)
	require.NoError(t, err)
	require.Equal(t, 1, loads[uriB], "another entry must survive an in-flight update")
}

func TestVisibleCatalogPrivateEntryNotificationsRetainOtherUser(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithCanonicalUserID(context.Background(), "alice")
	bob := authctx.WithCanonicalUserID(context.Background(), "bob")
	loads := map[string]int{}
	get := func(ctx context.Context, name string) {
		partition := mgr.WithCatalogSources(ctx, mgr.CatalogPartition(ctx, "verified", "navigation"), []string{"source"})
		_, err := mgr.VisibleCatalogEntryInPartition(ctx, name, partition, "navigation://shared/layout", func(context.Context) (json.RawMessage, time.Time, error) {
			loads[name]++
			return json.RawMessage(`{"private":true}`), time.Time{}, nil
		})
		require.NoError(t, err)
	}
	get(alice, "alice")
	get(bob, "bob")
	mgr.notifyResourceCatalogChange("source", mgr.resourceReceiver(alice), false)
	get(bob, "bob")
	get(alice, "alice")
	require.Equal(t, 1, loads["bob"], "private-only notification must retain otheruser's metadata")
	require.Equal(t, 2, loads["alice"])
}

func TestVisibleCatalogRepresentationParserCannotMutateCachedDefinition(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	partition := mgr.WithCatalogSources(ctx, mgr.PublicCatalogPartition("window"), []string{"source"})
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		return json.RawMessage(`{"original":true}`), time.Time{}, nil
	}
	_, err = mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "uri", load)
	require.NoError(t, err)
	parsed, err := mgr.VisibleCatalogRepresentation(ctx, "namespace", "uri", "immutable.schema", func(raw json.RawMessage) (any, error) { raw[0] = 'X'; return "detached", nil })
	require.NoError(t, err)
	require.Equal(t, "detached", parsed)
	raw, err := mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "uri", load)
	require.NoError(t, err)
	require.JSONEq(t, `{"original":true}`, string(raw))
	_, err = mgr.VisibleCatalogRepresentation(ctx, "namespace", "uri", "", func(json.RawMessage) (any, error) { t.Fatal("invalid schema parser ran"); return nil, nil })
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
	_, err = mgr.VisibleCatalogRepresentation(ctx, "namespace", "uri", "schema", nil)
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}

func TestRejectedRepresentationEvictsOnlyItsRawPayloadAndAliases(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	partition := mgr.PublicCatalogPartition("window")
	loads := 0
	load := func(context.Context) (json.RawMessage, time.Time, error) {
		loads++
		return json.RawMessage(`{"repaired":true}`), time.Time{}, nil
	}
	_, err = mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "broken", func(context.Context) (json.RawMessage, time.Time, error) {
		return json.RawMessage(`{"invalid":true}`), time.Time{}, nil
	}, func(json.RawMessage) []string { return []string{"broken@working"} })
	require.NoError(t, err)
	_, err = mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "healthy", load)
	require.NoError(t, err)
	_, err = mgr.VisibleCatalogRepresentation(ctx, "namespace", "broken", "window.schema", func(json.RawMessage) (any, error) { return nil, fmt.Errorf("invalid target binding") })
	require.Error(t, err)
	_, err = mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "healthy", load)
	require.NoError(t, err)
	require.Equal(t, 1, loads, "a different valid definition must remain cached")
	raw, err := mgr.VisibleCatalogEntryInPartition(ctx, "namespace", partition, "broken@working", load)
	require.NoError(t, err)
	require.JSONEq(t, `{"repaired":true}`, string(raw))
	require.Equal(t, 2, loads, "the rejected revision alias must fetch corrected provider bytes")
}
