package manager

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/mcp/client/config"
)

type nativeInventoryFixture struct{ calls []string }

func (*nativeInventoryFixture) Names(context.Context) ([]string, error) {
	return []string{"cohort", "ordinary"}, nil
}
func (*nativeInventoryFixture) CatalogNames(context.Context) ([]string, error) {
	return []string{"cohort"}, nil
}
func (p *nativeInventoryFixture) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	p.calls = append(p.calls, name)
	if name == "cohort" {
		return nil, errors.New("cohort options must not be inspected")
	}
	return &cfg.MCPClient{}, nil
}
func TestNamesExcludingRetainsOrdinaryNativeConnectionsBeforeOptionInspection(t *testing.T) {
	provider := &nativeInventoryFixture{}
	mgr, err := New(provider)
	require.NoError(t, err)
	names, err := mgr.NamesExcluding(context.Background(), []string{"cohort"})
	require.NoError(t, err)
	require.Equal(t, []string{"ordinary"}, names)
	require.Equal(t, []string{"ordinary"}, provider.calls)
}
