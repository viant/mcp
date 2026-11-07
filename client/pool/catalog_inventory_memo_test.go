package manager

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	auth "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
	"testing"
)

type countedCatalogInventory struct {
	lists, options int
	generation     string
}

func (p *countedCatalogInventory) Names(context.Context) ([]string, error) {
	return []string{"root", "tool"}, nil
}
func (p *countedCatalogInventory) CatalogNames(context.Context) ([]string, error) {
	p.lists++
	return []string{"root"}, nil
}
func (p *countedCatalogInventory) Options(context.Context, string) (*cfg.MCPClient, error) {
	p.options++
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "owner"}, nil
}
func (p *countedCatalogInventory) DefinitionGeneration(context.Context) string { return p.generation }
func TestCatalogRoutingMemoAvoidsRepeatedInventoryAndConfiguration(t *testing.T) {
	p := &countedCatalogInventory{generation: "1"}
	m, err := New(p)
	require.NoError(t, err)
	ctx := WithNamesMemo(auth.InjectUser(context.Background(), "alice"))
	for i := 0; i < 100; i++ {
		names, err := m.CatalogProviderNames(ctx, "owner")
		require.NoError(t, err)
		require.Equal(t, []string{"root"}, names)
		names[0] = "caller-mutation"
	}
	require.Equal(t, 1, p.lists)
	require.Equal(t, 2, p.options, "one browser-filter read and one owner-index read, not one scan per nested operation")
	for i, changed := range []context.Context{auth.InjectUser(ctx, "bob"), WithNamesMemo(auth.InjectUser(context.Background(), "alice"))} {
		_, err = m.CatalogProviderNames(changed, "owner")
		require.NoError(t, err)
		require.Equal(t, i+2, p.lists)
	}
	p.generation = "2"
	_, err = m.CatalogProviderNames(ctx, "owner")
	require.NoError(t, err)
	require.Equal(t, 4, p.lists)
	names, err := m.Names(ctx)
	require.NoError(t, err)
	require.Contains(t, names, "tool", fmt.Sprint("tool inventory remains separate"))
}
