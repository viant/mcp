package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	client "github.com/viant/mcp/client"
	cfg "github.com/viant/mcp/client/config"
)

func TestCatalogNamesExcludingSkipsCoveredOptionsButRetainsLocalCollisions(t *testing.T) {
	ctx := context.Background()
	provider := &countedCatalogInventory{}
	manager, err := New(provider)
	require.NoError(t, err)
	names, err := manager.CatalogNamesExcluding(WithNamesMemo(ctx), []string{"root"})
	require.NoError(t, err)
	require.Empty(t, names)
	require.Zero(t, provider.options)
	names, err = manager.CatalogNamesExcluding(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"root"}, names)
	require.Equal(t, 1, provider.options)
	// Simulate a late configured collision against a registered local name.
	require.NoError(t, manager.RegisterLocal(ctx, "local", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "local"}, func(context.Context) (client.Interface, error) { return nil, nil }))
	manager.localMu.Lock()
	manager.local["root"] = manager.local["local"]
	manager.localMu.Unlock()
	_, err = manager.CatalogNamesExcluding(ctx, []string{"root"})
	require.Error(t, err)
}
