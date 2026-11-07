package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparedToolCatalogReadsDoNotDecodeWireAgain(t *testing.T) {
	_, _, source, managed, _ := catalogSetup(t, true)
	ctx := WithToolCatalogReadSnapshot(context.Background())
	first, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	_, err = managed.ListTools(ctx, first.NextCursor)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		result, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
		require.Equal(t, "original", result.Tools[0].Meta["custom"].(map[string]interface{})["nested"])
		result.Tools[0].Meta["custom"].(map[string]interface{})["nested"] = "changed"
		identity, tools, err := managed.toolCatalogSnapshot(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, identity)
		require.Len(t, tools, 2)
		require.Equal(t, "original", tools[0].Meta["custom"].(map[string]interface{})["nested"])
		tools[0].Meta["custom"].(map[string]interface{})["nested"] = "snapshot changed"
	}
	require.EqualValues(t, 2, source.calls.Load())
}

func TestPreparedToolCatalogCopiesNestedSchemasAndMetadata(t *testing.T) {
	content, err := prepareToolCatalogContent([]byte(`{"tools":[{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string","enum":["one","two"]}},"$defs":{"id":{"type":"integer"}}},"outputSchema":{"type":"object","properties":{"rows":{"type":"array"}}},"_meta":{"custom":{"list":[{"value":"original"}]}}}]}`))
	require.NoError(t, err)
	first, err := content.Copy()
	require.NoError(t, err)
	first.Tools[0].InputSchema.Properties["query"]["enum"].([]interface{})[0] = "changed"
	first.Tools[0].Meta["custom"].(map[string]interface{})["list"].([]interface{})[0].(map[string]interface{})["value"] = "changed"
	second, err := content.Copy()
	require.NoError(t, err)
	require.Equal(t, "one", second.Tools[0].InputSchema.Properties["query"]["enum"].([]interface{})[0])
	third, err := content.Copy()
	require.NoError(t, err)
	require.Equal(t, third, second)
}

func TestPreparedToolCatalogSelectionPreservesIdentityAndIsolation(t *testing.T) {
	_, _, source, managed, other := catalogSetup(t, false)
	ctx := context.Background()
	first, err := managed.ListTools(ctx, nil)
	require.NoError(t, err)
	_, err = managed.ListTools(ctx, first.NextCursor)
	require.NoError(t, err)
	identity, _, err := managed.toolCatalogSnapshot(ctx)
	require.NoError(t, err)
	for _, alias := range []string{"generic/tool", "server/generic/tool", "server:generic/tool", "server_generic-tool", "server-generic_tool"} {
		selected := &toolCatalogSelection{names: []string{alias}, metadataOnly: true}
		gotIdentity, tools, err := managed.selectedToolCatalogState(ctx, true, selected)
		require.NoError(t, err)
		require.Equal(t, identity, gotIdentity)
		require.Len(t, tools, 1, alias)
		require.Equal(t, "generic/tool", tools[0].Name)
		tools[0].Meta["custom"].(map[string]interface{})["nested"] = "caller change"
		unavailable, hidden, err := other.selectedToolCatalogState(ctx, true, selected)
		require.NoError(t, err)
		require.Empty(t, unavailable)
		require.Empty(t, hidden)
	}
	same, missing, err := managed.selectedToolCatalogState(ctx, true, &toolCatalogSelection{names: []string{"missing"}})
	require.NoError(t, err)
	require.Equal(t, identity, same)
	require.Empty(t, missing)
	require.EqualValues(t, 2, source.calls.Load())
}

func TestHTTPPreparedCatalogSelectCopiesOnlyRequestedShape(t *testing.T) {
	f := newIdentityFixture(t, identityFixtureOptions{persistent: true, large: true})
	ctx := identityUser("alice", "a")
	client, err := f.manager.Get(ctx, "", "server")
	require.NoError(t, err)
	source, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	name := source.Tools[0].Name
	fullIdentity, full, err := f.manager.ToolCatalogSelect(ctx, "", "server", []string{name}, false)
	require.NoError(t, err)
	require.Len(t, full, 1)
	require.Equal(t, source.Tools[0], full[0])
	metaIdentity, metadata, err := f.manager.ToolCatalogSelect(ctx, "", "server", []string{name}, true)
	require.NoError(t, err)
	require.Equal(t, fullIdentity, metaIdentity)
	require.Len(t, metadata, 1)
	require.Empty(t, metadata[0].InputSchema.Properties)
	require.Nil(t, metadata[0].OutputSchema)
	require.Equal(t, full[0].Meta, metadata[0].Meta)
	denied, hidden, err := f.manager.ToolCatalogSelect(identityUser("bob", "b"), "", "server", []string{name}, false)
	require.NoError(t, err)
	require.Empty(t, denied)
	require.Empty(t, hidden)
	f.manager.mu.Lock()
	f.manager.invalidateToolCatalogLocked("server")
	f.manager.mu.Unlock()
	stale, removed, err := f.manager.ToolCatalogSelect(ctx, "", "server", []string{name}, false)
	require.NoError(t, err)
	require.Empty(t, stale)
	require.Empty(t, removed)
	require.EqualValues(t, 1, f.lists.Load(), "selected reads must never fetch or refresh the source")
}
