package manager

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVisibleCatalogRefreshReplacesEntryAndAliasesWithoutMutatingHeldMetadata(t *testing.T) {
	manager, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	partition := manager.PublicCatalogPartition("report")
	old := time.Now().Add(3 * time.Second)
	fresh := time.Now().Add(5 * time.Minute)
	calls := 0
	load := func(until time.Time) func(context.Context) (json.RawMessage, time.Time, error) {
		return func(context.Context) (json.RawMessage, time.Time, error) {
			calls++
			raw, _ := json.Marshal(map[string]string{"validUntil": until.Format(time.RFC3339Nano)})
			return raw, until, nil
		}
	}
	held, err := manager.VisibleCatalogEntryInPartition(ctx, "sample", partition, "report\x00", load(old), func(json.RawMessage) []string { return []string{"report\x00working"} })
	require.NoError(t, err)
	refreshed, err := manager.RefreshVisibleCatalogEntryInPartition(ctx, "sample", partition, "report\x00working", load(fresh))
	require.NoError(t, err)
	require.NotEqual(t, string(held), string(refreshed))
	require.Contains(t, string(held), old.Format(time.RFC3339Nano))
	for _, key := range []string{"report\x00", "report\x00working"} {
		warm, err := manager.VisibleCatalogEntryInPartition(ctx, "sample", partition, key, load(old))
		require.NoError(t, err)
		require.JSONEq(t, string(refreshed), string(warm))
	}
	require.Equal(t, 2, calls, "explicit refresh must replace both selector aliases")
	require.Contains(t, string(held), old.Format(time.RFC3339Nano), "detached old grants retain original deadline")
}

func TestVisibleCatalogRefreshKeepsPublicationFenceOnConcurrentInvalidation(t *testing.T) {
	manager, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	partition := manager.PublicCatalogPartition("report")
	old := json.RawMessage(`{"deadline":"original"}`)
	held, err := manager.VisibleCatalogEntryInPartition(ctx, "sample", partition, "sample", func(context.Context) (json.RawMessage, time.Time, error) {
		return old, time.Now().Add(time.Minute), nil
	})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := manager.RefreshVisibleCatalogEntryInPartition(ctx, "sample", partition, "sample", func(context.Context) (json.RawMessage, time.Time, error) {
			close(entered)
			<-release
			return json.RawMessage(`{"deadline":"new"}`), time.Now().Add(5 * time.Minute), nil
		})
		done <- err
	}()
	<-entered
	manager.InvalidateVisibleCatalogPartition(partition)
	close(release)
	require.Error(t, <-done, "invalidated refresh cannot publish a new grant")
	require.JSONEq(t, string(old), string(held), "old detached metadata is immutable")
}
