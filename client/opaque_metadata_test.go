package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	authcfg "github.com/viant/mcp/client/auth/config"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// Exercise the actual JSON-RPC request produced by CallTool, rather than a
// vendor-specific metadata decoder. Explicit extension metadata is opaque.
func TestGenericOpaqueCallerMetadataOnActualRPC(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			calls := 0
			tr := &mockTransport{send: func(ctx context.Context, req *jsonrpc.Request) (*jsonrpc.Response, error) {
				calls++
				require.Equal(t, schema.MethodToolsCall, req.Method)
				require.True(t, authcfg.NoRetry(ctx))
				require.Equal(t, "synthetic-session-token", ctx.Value(authtransport.ContextAuthTokenKey))
				var wire map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(req.Params, &wire))
				var meta map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(wire["_meta"], &meta))
				require.JSONEq(t, `{"revision":"opaque-release","nested":{"value":"opaque"}}`, string(meta["arbitrary_meta"]))
				require.JSONEq(t, `{"revision":"caller-pinned"}`, string(meta["example.org/binding"]))
				require.NotContains(t, string(wire["arguments"]), "arbitrary_meta")
				return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}, nil
			}}
			c := New("generic-client", "1", tr, WithProtocolVersion(version), WithMetadata(map[string]any{"example.org/binding": map[string]any{"revision": "client-default"}}))
			c.initialized = true
			params := &schema.CallToolRequestParams{Name: "read", Arguments: map[string]any{"id": "business"}}
			require.NoError(t, json.Unmarshal([]byte(`{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"arbitrary_meta":{"revision":"opaque-release","nested":{"value":"opaque"}},"example.org/binding":{"revision":"caller-pinned"}}`), &params.Meta))
			before, err := json.Marshal(params)
			require.NoError(t, err)
			_, err = c.CallTool(context.Background(), params, WithAuthToken("synthetic-session-token"), WithNoRetry())
			require.NoError(t, err)
			after, err := json.Marshal(params)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
			require.Equal(t, 1, calls)
		})
	}
}

func TestGenericCatalogPagesAndCallsStayInClientAuthScope(t *testing.T) {
	for _, scope := range []string{"catalog-a", "catalog-b"} {
		t.Run(scope, func(t *testing.T) {
			lists, calls := 0, 0
			tr := &mockTransport{send: func(ctx context.Context, req *jsonrpc.Request) (*jsonrpc.Response, error) {
				require.Equal(t, scope, ctx.Value(authtransport.ContextAuthTokenKey))
				var params map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(req.Params, &params))
				var result any
				switch req.Method {
				case schema.MethodToolsList:
					lists++
					var cursor string
					if raw := params["cursor"]; len(raw) > 0 {
						require.NoError(t, json.Unmarshal(raw, &cursor))
					}
					if lists == 1 {
						require.Empty(t, cursor)
						result = map[string]any{"tools": []any{map[string]any{"name": "first", "inputSchema": map[string]any{"type": "object"}, "_meta": map[string]any{"arbitrary_meta": scope}}}, "nextCursor": "second"}
					} else {
						require.Equal(t, "second", cursor)
						result = map[string]any{"tools": []any{map[string]any{"name": "read", "inputSchema": map[string]any{"type": "object"}, "_meta": map[string]any{"arbitrary_meta": scope}}}}
					}
				case schema.MethodToolsCall:
					calls++
					require.True(t, authcfg.NoRetry(ctx))
					var name string
					require.NoError(t, json.Unmarshal(params["name"], &name))
					require.Equal(t, "read", name)
					var meta map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(params["_meta"], &meta))
					require.NotContains(t, meta, "arbitrary_meta") // annotations are NOT silently sent as request extensions
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": scope}}}
				default:
					t.Fatalf("unexpected RPC %s", req.Method)
				}
				raw, err := json.Marshal(result)
				require.NoError(t, err)
				return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: raw}, nil
			}}
			c := New("generic-client", "1", tr, WithProtocolVersion(schema.LatestProtocolVersion))
			c.initialized = true
			page1, err := c.ListTools(context.Background(), nil, WithAuthToken(scope))
			require.NoError(t, err)
			require.Equal(t, scope, page1.Tools[0].Meta["arbitrary_meta"])
			page2, err := c.ListTools(context.Background(), page1.NextCursor, WithAuthToken(scope))
			require.NoError(t, err)
			require.Equal(t, scope, page2.Tools[0].Meta["arbitrary_meta"])
			result, err := c.CallTool(context.Background(), &schema.CallToolRequestParams{Name: page2.Tools[0].Name, Arguments: map[string]any{"query": "ordinary"}}, WithAuthToken(scope), WithNoRetry())
			require.NoError(t, err)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.Contains(t, string(raw), scope)
			require.Equal(t, 2, lists)
			require.Equal(t, 1, calls)
		})
	}
}
