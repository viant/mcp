package manager

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	primitive "github.com/viant/mcp/client/catalog/index"
	"testing"
	"time"
)

func TestVisibleCatalogSourceLeaseBoundsUncachedDelivery(t *testing.T) {
	m, err := New(nil)
	require.NoError(t, err)
	ctx := context.Background()
	lease := time.Now().Add(30 * time.Millisecond)
	index, err := m.VisibleCatalog(ctx, "uncached-source", time.Time{}, func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		return []primitive.CatalogItem[json.RawMessage]{{URI: "toolfeed://owned/a", Value: json.RawMessage(`{}`)}}, nil
	}, func() time.Time { return lease })
	require.NoError(t, err)
	require.NoError(t, m.CheckVisibleCatalog(ctx, index))
	time.Sleep(40 * time.Millisecond)
	require.ErrorIs(t, m.CheckVisibleCatalog(ctx, index), primitive.ErrCatalogCursor)
	_, err = m.VisibleCatalog(ctx, "expired-source", time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil }, func() time.Time { return time.Now().Add(-time.Millisecond) })
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}
