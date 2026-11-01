package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp-protocol/schema"
)

func TestToolResultFinalizationPreservesExtensionsAndMergesOnlyMissingServerInfo(t *testing.T) {
	handler := &Handler{Server: &Server{info: schema.Implementation{Name: "host", Version: "1"}}}
	for _, version := range []string{schema.LatestProtocolVersion, schema.LegacyProtocolVersion} {
		for _, explicit := range []bool{false, true} {
			original := &schema.ResultMetaObject{AdditionalProperties: map[string]any{"example/http-status": 403, "example/flag": true}}
			if explicit {
				original.IoModelcontextprotocolServerInfo = &schema.Implementation{Name: "declared", Version: "2"}
			}
			result := &schema.CallToolResult{Meta: original, Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: `{"status":"error"}`}}}
			handler.finalizeResult(version, result)
			require.Equal(t, original.AdditionalProperties, result.Meta.AdditionalProperties)
			switch {
			case explicit:
				require.Equal(t, "declared", result.Meta.IoModelcontextprotocolServerInfo.Name)
			case version == schema.LatestProtocolVersion:
				require.Equal(t, "host", result.Meta.IoModelcontextprotocolServerInfo.Name)
				require.Nil(t, original.IoModelcontextprotocolServerInfo)
			default:
				require.Nil(t, result.Meta.IoModelcontextprotocolServerInfo)
			}
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"example/http-status":403`)
		}
	}
}
