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

func TestListResourcesWithParamsActualRPC(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			calls := 0
			tr := &mockTransport{send: func(ctx context.Context, req *jsonrpc.Request) (*jsonrpc.Response, error) {
				calls++
				require.Equal(t, schema.MethodResourcesList, req.Method)
				require.Equal(t, "catalog-scope", ctx.Value(authtransport.ContextAuthTokenKey))
				require.True(t, authcfg.NoRetry(ctx))
				var wire struct {
					Cursor string                     `json:"cursor"`
					Meta   map[string]json.RawMessage `json:"_meta"`
				}
				require.NoError(t, json.Unmarshal(req.Params, &wire))
				switch calls {
				case 1:
					require.Equal(t, "first-page", wire.Cursor)
					require.JSONEq(t, `{"kind":"window","nested":{"enabled":true}}`, string(wire.Meta["io.viant/resources/filter"]))
					require.JSONEq(t, `{"value":"caller"}`, string(wire.Meta["example.org/opaque"]))
				case 2:
					require.Equal(t, "next-page", wire.Cursor)
					require.NotContains(t, wire.Meta, "io.viant/resources/filter")
				case 3:
					require.Empty(t, wire.Cursor)
					require.NotContains(t, wire.Meta, "io.viant/resources/filter")
				default:
					t.Fatalf("unexpected request %d", calls)
				}
				if version == schema.LatestProtocolVersion {
					require.JSONEq(t, `"`+version+`"`, string(wire.Meta["io.modelcontextprotocol/protocolVersion"]))
					require.Contains(t, wire.Meta, "io.modelcontextprotocol/clientCapabilities")
				}
				return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"resources":[{"uri":"test://window","name":"window"}],"nextCursor":"next-page"}`)}, nil
			}}
			c := New("resource-client", "1", tr, WithProtocolVersion(version), WithMetadata(map[string]any{"example.org/opaque": map[string]any{"value": "default"}}))
			c.initialized = true
			params := &schema.ListResourcesRequestParams{}
			require.NoError(t, json.Unmarshal([]byte(`{"cursor":"first-page","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.viant/resources/filter":{"kind":"window","nested":{"enabled":true}},"example.org/opaque":{"value":"caller"}}}`), params))
			before, err := json.Marshal(params)
			require.NoError(t, err)
			result, err := c.ListResourcesWithParams(context.Background(), params, WithAuthToken("catalog-scope"), WithNoRetry())
			require.NoError(t, err)
			require.Equal(t, "test://window", result.Resources[0].Uri)
			after, err := json.Marshal(params)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
			_, err = c.ListResources(context.Background(), result.NextCursor, WithAuthToken("catalog-scope"), WithNoRetry())
			require.NoError(t, err)
			_, err = c.ListResourcesWithParams(context.Background(), nil, WithAuthToken("catalog-scope"), WithNoRetry())
			require.NoError(t, err)
			require.Equal(t, 3, calls)
		})
	}
}
