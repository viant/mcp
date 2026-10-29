package client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	"testing"
)

func TestNumericArgumentsSurviveReconnectAndProtocolStartup(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			reconnects, starts, calls := 0, 0, 0
			first := &mockTransport{send: func(_ context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
				return nil, errors.New("session 'old' not found")
			}}
			replacement := &mockTransport{send: func(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
				var result interface{}
				switch r.Method {
				case schema.MethodServerDiscover:
					require.Equal(t, schema.LatestProtocolVersion, version)
					starts++
					result = map[string]interface{}{"resultType": "complete", "cacheScope": "public", "ttlMs": 0, "capabilities": map[string]interface{}{}, "supportedVersions": []string{version}}
				case schema.MethodInitialize:
					require.Equal(t, schema.LegacyProtocolVersion, version)
					starts++
					result = map[string]interface{}{"protocolVersion": version, "capabilities": map[string]interface{}{}, "serverInfo": map[string]interface{}{"name": "legacy", "version": "1"}}
				case schema.MethodToolsCall:
					calls++
					var params schema.CallToolRequestParams
					require.NoError(t, json.Unmarshal(r.Params, &params))
					require.Equal(t, float64(7), params.Arguments["small"])
					require.Equal(t, float64(0.1), params.Arguments["fraction"])
					require.Equal(t, float64(1000), params.Arguments["exponent"])
					require.Equal(t, json.Number("9007199254740993"), params.Arguments["large"])
					result = map[string]interface{}{"content": []interface{}{}, "structuredContent": params.Arguments}
				default:
					t.Fatalf("unexpected method: %s", r.Method)
				}
				raw, err := json.Marshal(result)
				require.NoError(t, err)
				return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Id: r.Id, Result: raw}, nil
			}}
			c := New("numeric", "1", first, WithProtocolVersion(version), WithReconnect(func(context.Context) (transport.Transport, error) { reconnects++; return replacement, nil }))
			defer c.Close()
			c.initialized = true
			result, err := c.CallTool(context.Background(), &schema.CallToolRequestParams{Meta: schema.RequestMetaObject{IoModelcontextprotocolProtocolVersion: version}, Name: "numeric", Arguments: map[string]interface{}{"small": 7, "fraction": 0.1, "exponent": json.Number("1e3"), "large": json.Number("9007199254740993")}})
			require.NoError(t, err)
			require.Equal(t, 1, reconnects)
			require.Equal(t, 1, starts)
			require.Equal(t, 1, calls)
			values := result.StructuredContent.(map[string]interface{})
			require.Equal(t, float64(7), values["small"])
			require.Equal(t, json.Number("9007199254740993"), values["large"])
		})
	}
}
