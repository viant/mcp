package catalog

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPreparedToolCatalogCopiesNestedSchemasAndMetadata(t *testing.T) {
	content, err := New([]byte(`{"tools":[{"name":"lookup","inputSchema":{"type":"object","properties":{"query":{"type":"string","enum":["one","two"]}},"$defs":{"id":{"type":"integer"}}},"outputSchema":{"type":"object","properties":{"rows":{"type":"array"}}},"_meta":{"custom":{"list":[{"value":"original"}]}}}]}`))
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

func TestPageOwnsWireAndSelectedMetadata(t *testing.T) {
	wire := []byte(`{"tools":[{"name":"lookup","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}},"_meta":{"value":"original"}}]}`)
	page, err := New(wire, "source")
	require.NoError(t, err)
	for i := range wire {
		wire[i] = '!'
	}
	for _, alias := range []string{"lookup", "source/lookup", "source:lookup", "source-lookup"} {
		tools, err := page.Select([]string{alias}, true)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		require.Empty(t, tools[0].InputSchema.Properties)
		require.Equal(t, "original", tools[0].Meta["value"])
		tools[0].Meta["value"] = "changed"
	}
	full, err := page.Select([]string{"lookup"}, false)
	require.NoError(t, err)
	require.NotEmpty(t, full[0].InputSchema.Properties)
	none, err := page.Select([]string{}, false)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestURIProviderUsesModelSafeAliasWithoutReconstructingAddress(t *testing.T) {
	page, err := New([]byte(`{"tools":[{"name":"Fetch","inputSchema":{"type":"object"}}]}`), "mcp://example/source")
	require.NoError(t, err)
	tools, err := page.Select([]string{"mcp___example_source-Fetch"}, true)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "Fetch", tools[0].Name)
}
