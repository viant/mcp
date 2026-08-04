package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func TestJulyClientDiscoversAndAddsPerRequestMetadata(t *testing.T) {
	var methods []string
	transport := &mockTransport{send: func(ctx context.Context, request *jsonrpc.Request) (*jsonrpc.Response, error) {
		methods = append(methods, request.Method)
		var params map[string]interface{}
		require.NoError(t, json.Unmarshal(request.Params, &params))
		meta, ok := params["_meta"].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, schema.LatestProtocolVersion, meta["io.modelcontextprotocol/protocolVersion"])
		require.NotNil(t, meta["io.modelcontextprotocol/clientCapabilities"])
		require.NotNil(t, meta["io.modelcontextprotocol/clientInfo"])
		if request.Method == schema.MethodToolsList {
			require.Equal(t, string(schema.LoggingLevelInfo), meta["io.modelcontextprotocol/logLevel"])
		}

		var result interface{}
		switch request.Method {
		case schema.MethodServerDiscover:
			result = &schema.DiscoverResult{
				CacheScope:        schema.DiscoverResultCacheScopePublic,
				Capabilities:      schema.ServerCapabilities{},
				ResultType:        "complete",
				SupportedVersions: []string{schema.LatestProtocolVersion, schema.LegacyProtocolVersion},
				TtlMs:             0,
			}
		case schema.MethodToolsList:
			result = &schema.ListToolsResult{
				CacheScope: schema.ListToolsResultCacheScopePrivate,
				ResultType: "complete",
				Tools:      []schema.Tool{},
				TtlMs:      0,
			}
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		raw, err := json.Marshal(result)
		require.NoError(t, err)
		return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: raw}, nil
	}}

	client := New("july-client", "1.0.0", transport, WithProtocolVersion(schema.LatestProtocolVersion))
	initialized, err := client.Initialize(context.Background())
	require.NoError(t, err)
	require.Equal(t, schema.LatestProtocolVersion, initialized.ProtocolVersion)
	_, err = client.SetLevel(context.Background(), &schema.SetLevelRequestParams{Level: schema.LoggingLevelInfo})
	require.NoError(t, err)
	_, err = client.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{schema.MethodServerDiscover, schema.MethodToolsList}, methods)
}
