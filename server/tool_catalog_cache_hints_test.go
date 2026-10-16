package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func TestToolCatalogWireAndAdapterPreserveLegacyAbsenceAndExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, version, scope string
		ttl                  int
		missing              bool
	}{
		{name: "ordinary legacy tool producer", version: schema.LegacyProtocolVersion, missing: true},
		{name: "older legacy tool producer", version: "2025-06-18", missing: true},
		{name: "private zero remains explicit", version: schema.LegacyProtocolVersion, scope: "private"},
		{name: "public zero remains explicit", version: schema.LegacyProtocolVersion, scope: "public"},
		{name: "private positive remains explicit", version: schema.LegacyProtocolVersion, scope: "private", ttl: 60000},
		{name: "July requires conservative metadata", version: schema.LatestProtocolVersion, scope: "private"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read", InputSchema: schema.ToolInputSchema{Type: "object"}}}, TtlMs: tc.ttl}
			if tc.version != schema.LatestProtocolVersion {
				source.CacheScope = schema.ListToolsResultCacheScope(tc.scope)
			}
			response := &jsonrpc.Response{}
			(&Handler{Server: &Server{}}).setResponse(tc.version, response, source, nil)
			require.Nil(t, response.Error)
			var wire map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Result, &wire))
			if tc.missing {
				require.NotContains(t, wire, "cacheScope")
				require.NotContains(t, wire, "ttlMs")
				require.Empty(t, source.CacheScope)
			} else {
				require.Contains(t, wire, "cacheScope")
				require.Contains(t, wire, "ttlMs")
			}
			var adapted schema.ListToolsResult
			require.NoError(t, decodeAdapterResult(response.Result, &adapted))
			require.Equal(t, tc.scope, string(adapted.CacheScope))
			require.Equal(t, tc.ttl, adapted.TtlMs)
			require.Len(t, adapted.Tools, 1)
		})
	}
}
