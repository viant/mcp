package manager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/mcp/client/auth/link"
	primitive "github.com/viant/mcp/client/catalog/index"
	requestctx "github.com/viant/mcp/client/requestctx"
)

func TestVisibleCatalogPartitionInvalidationKeepsOtherOwnersAndCategories(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	partition := m.CatalogPartition(alice, "verified-account", "report")
	rotated := requestctx.WithConversationID(authctx.WithBearer(alice, "rotated"), "other-session")
	require.Equal(t, partition, m.CatalogPartition(rotated, "verified-account", "report"), "credential/session changes must not hide the same owner's old lists")
	canonical := authctx.WithCanonicalUserID(alice, "canonical-alice")
	canonicalPartition := m.CatalogPartition(canonical, "account/revision-one", "report")
	require.Equal(t, canonicalPartition, m.CatalogPartition(authctx.WithProvider(authctx.WithBearer(canonical, "rotated"), "other-verified-entry"), "account/revision-two", "report"), "canonical ownership must survive account/revision and verified entry changes")
	require.NotEqual(t, canonicalPartition, m.CatalogPartition(authctx.WithCanonicalUserID(bob, "canonical-bob"), "account/revision-one", "report"))
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		return []primitive.CatalogItem[json.RawMessage]{{URI: "report://user/first", Name: "first", Value: json.RawMessage(`{"name":"first"}`)}, {URI: "report://user/second", Name: "second", Value: json.RawMessage(`{"name":"second"}`)}}, nil
	}
	metadata, err := m.VisibleCatalogInPartition(alice, "metadata", partition, time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	hydrated, err := m.VisibleCatalogInPartition(alice, "hydrated", partition, time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	other, err := m.VisibleCatalogInPartition(bob, "other-owner", m.CatalogPartition(bob, "verified-account", "report"), time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	different, err := m.VisibleCatalogInPartition(alice, "other-category", m.CatalogPartition(alice, "verified-account", "intent"), time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	first, err := metadata.Page(alice, primitive.CatalogQuery{Limit: 1})
	require.NoError(t, err)
	m.InvalidateVisibleCatalogPartition(partition)
	require.ErrorIs(t, m.CheckVisibleCatalog(alice, metadata), primitive.ErrCatalogCursor)
	require.ErrorIs(t, m.CheckVisibleCatalog(alice, hydrated), primitive.ErrCatalogCursor)
	require.NoError(t, m.CheckVisibleCatalog(bob, other))
	require.NoError(t, m.CheckVisibleCatalog(alice, different))
	fresh, err := m.VisibleCatalogInPartition(alice, "metadata", partition, time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	_, err = fresh.Page(alice, primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor})
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}

func TestVisibleCatalogPartitionInvalidationFencesInflightPublication(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	partition := m.CatalogPartition(alice, "actor", "report")
	entered, release := make(chan struct{}), make(chan struct{})
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		close(entered)
		<-release
		return []primitive.CatalogItem[json.RawMessage]{{URI: "report://user/stale", Name: "stale"}}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.VisibleCatalogInPartition(alice, "alice", partition, time.Now().Add(time.Minute), load)
		done <- err
	}()
	<-entered
	other, err := m.VisibleCatalogInPartition(bob, "bob", m.CatalogPartition(bob, "actor", "report"), time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil })
	require.NoError(t, err)
	m.InvalidateVisibleCatalogPartition(partition)
	fresh, err := m.VisibleCatalogInPartition(alice, "alice", partition, time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		return []primitive.CatalogItem[json.RawMessage]{{URI: "report://user/fresh", Name: "fresh", Value: json.RawMessage(`{"name":"fresh"}`)}}, nil
	})
	require.NoError(t, err)
	close(release)
	require.ErrorIs(t, <-done, primitive.ErrCatalogCursor, "late population must not replace the new snapshot")
	require.NoError(t, m.CheckVisibleCatalog(alice, fresh))
	require.NoError(t, m.CheckVisibleCatalog(bob, other))
	page, err := fresh.Page(alice, primitive.CatalogQuery{})
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"fresh"}`, string(page.Items[0]))
}
