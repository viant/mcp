package primitive

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestRankedCatalogSearchKeepsLiteralFiltersAndCursors(t *testing.T) {
	items := []CatalogItem[string]{{Name: "aaa", Description: "Compile documents", Namespace: "one", OwnerID: "alice", Value: "compile"}, {Name: "zzz", Description: "Inspect report filters and defaults", Namespace: "one", OwnerID: "alice", Value: "inspect"}, {Name: "hidden", Description: "Inspect report filters and defaults", Namespace: "two", OwnerID: "bob", Value: "other"}, {Name: "preview", Description: "Inspect report filters preview", Namespace: "one", OwnerID: "alice", Value: "preview"}}
	idx := NewCatalog("generation1", items)
	query := CatalogQuery{Search: "inspect report filters", Namespace: "one", OwnerIDs: []string{"alice"}, ExcludeKeywords: []string{"preview"}, Limit: 1}
	page, err := idx.Page(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, []string{"inspect"}, page.Items)
	require.Equal(t, 1, page.Total)
	require.True(t, page.Complete)
	query.Namespace = ""
	query.OwnerIDs = nil
	query.ExcludeKeywords = nil
	page, err = idx.Page(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.NotEmpty(t, page.NextCursor)
	query.Cursor = page.NextCursor
	next, err := idx.Page(context.Background(), query)
	require.NoError(t, err)
	require.NotEqual(t, page.Items, next.Items)
	query.Search = "compile"
	_, err = idx.Page(context.Background(), query)
	require.ErrorIs(t, err, ErrCatalogCursor)
	query.Search = "inspect report filters"
	_, err = NewCatalog("generation2", items).Page(context.Background(), query)
	require.ErrorIs(t, err, ErrCatalogCursor)
	literal, err := idx.Page(context.Background(), CatalogQuery{NameTerms: []string{"aaa"}})
	require.NoError(t, err)
	require.Equal(t, []string{"compile"}, literal.Items)
}
func TestRankedCatalogConcurrentNoHitAndCancellation(t *testing.T) {
	idx := NewCatalog("v1", []CatalogItem[string]{{Name: "find", Description: "Find documents", Value: "find"}})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p, e := idx.Page(context.Background(), CatalogQuery{Search: "documents"})
				if e != nil || len(p.Items) != 1 {
					t.Error(p, e)
				}
			}
		}()
	}
	wg.Wait()
	p, e := idx.Page(context.Background(), CatalogQuery{Search: "zzqunknown"})
	require.NoError(t, e)
	require.Zero(t, p.Total)
	require.Empty(t, p.Items)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = idx.Page(ctx, CatalogQuery{Search: "documents"})
	require.ErrorIs(t, e, context.Canceled)
}
