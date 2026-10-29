package client

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/internal/conv"
	"testing"
)

func TestCallToolMetadataPreservesNumericArguments(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		for _, auth := range []string{"", "synthetic-token"} {
			t.Run(version+auth, func(t *testing.T) {
				calls := 0
				tr := &mockTransport{send: func(_ context.Context, req *jsonrpc.Request) (*jsonrpc.Response, error) {
					calls++
					var wire struct {
						Arguments map[string]interface{} `json:"arguments"`
					}
					require.NoError(t, conv.DecodeNumbers(req.Params, &wire))
					require.Equal(t, json.Number("9007199254740993"), wire.Arguments["id"])
					require.Equal(t, json.Number("9007199254740995"), wire.Arguments["nested"].([]interface{})[0].(map[string]interface{})["id"])
					require.Equal(t, false, wire.Arguments["flag"])
					require.Nil(t, wire.Arguments["null"])
					return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}, nil
				}}
				c := New("numeric", "1", tr, WithProtocolVersion(version))
				c.initialized = true
				_, err := c.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "exact", Arguments: map[string]interface{}{"id": json.Number("9007199254740993"), "nested": []interface{}{map[string]interface{}{"id": json.Number("9007199254740995")}}, "flag": false, "null": nil}}, WithAuthToken(auth))
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			})
		}
	}
}
