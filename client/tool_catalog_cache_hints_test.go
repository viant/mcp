package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func TestListToolsPreservesLegacyAbsenceAndExplicitCacheHints(t *testing.T) {
	for _, tc := range []struct {
		name, hints, scope string
		ttl                int
	}{
		{name: "ordinary legacy server without cache metadata"},
		{name: "empty legacy scope", hints: `,"cacheScope":""`},
		{name: "explicit private zero", hints: `,"cacheScope":"private","ttlMs":0`, scope: "private"},
		{name: "explicit public zero", hints: `,"cacheScope":"public","ttlMs":0`, scope: "public"},
		{name: "explicit zero without scope", hints: `,"ttlMs":0`, scope: "private"},
		{name: "positive ttl without scope", hints: `,"ttlMs":1000`, scope: "private", ttl: 1000},
		{name: "explicit private positive", hints: `,"cacheScope":"private","ttlMs":60000`, scope: "private", ttl: 60000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			transport := &mockTransport{send: func(_ context.Context, request *jsonrpc.Request) (*jsonrpc.Response, error) {
				if request.Method == schema.MethodInitialize {
					return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"legacy","version":"1"}}`)}, nil
				}
				require.Equal(t, schema.MethodToolsList, request.Method)
				calls++
				return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"tools":[{"name":"read","inputSchema":{"type":"object"}}]` + tc.hints + `}`)}, nil
			}}
			client := New("legacy-tools", "1", transport, WithProtocolVersion("2025-11-25"))
			result, err := client.ListTools(context.Background(), nil)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Len(t, result.Tools, 1)
			require.Equal(t, "complete", result.ResultType)
			require.Equal(t, tc.scope, string(result.CacheScope))
			require.Equal(t, tc.ttl, result.TtlMs)
		})
	}
}
