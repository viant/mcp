package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func TestPrepareProtocolRequestKeepsCapabilitiesRequestScoped(t *testing.T) {
	handler := &Handler{}
	withRoots := true
	first := julyRequestParams(t, schema.ClientCapabilities{Roots: &schema.ClientCapabilitiesRoots{ListChanged: &withRoots}})
	firstCtx, version, rpcErr := handler.prepareProtocolRequest(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsList, Params: first})
	require.Nil(t, rpcErr)
	require.Equal(t, schema.LatestProtocolVersion, version)
	firstInfo, ok := ProtocolRequestFromContext(firstCtx)
	require.True(t, ok)
	require.NotNil(t, firstInfo.Capabilities.Roots)

	second := julyRequestParams(t, schema.ClientCapabilities{})
	secondCtx, _, rpcErr := handler.prepareProtocolRequest(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsList, Params: second})
	require.Nil(t, rpcErr)
	secondInfo, ok := ProtocolRequestFromContext(secondCtx)
	require.True(t, ok)
	require.Nil(t, secondInfo.Capabilities.Roots)
	require.NotNil(t, firstInfo.Capabilities.Roots)
	require.Nil(t, handler.clientInitialize)
}

func TestPrepareProtocolRequestNormalizesLegacyMeta(t *testing.T) {
	testCases := []struct {
		name string
		meta map[string]interface{}
	}{
		{
			name: "progress token only",
			meta: map[string]interface{}{"progressToken": float64(1)},
		},
		{
			name: "explicit legacy version",
			meta: map[string]interface{}{
				"io.modelcontextprotocol/protocolVersion": schema.LegacyProtocolVersion,
				"progressToken": float64(1),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]interface{}{
				"_meta":     testCase.meta,
				"arguments": map[string]interface{}{},
				"name":      "AdHierarchy",
			})
			require.NoError(t, err)
			request := &jsonrpc.Request{Method: schema.MethodToolsCall, Params: raw}

			_, version, rpcErr := (&Handler{}).prepareProtocolRequest(context.Background(), request)
			require.Nil(t, rpcErr)
			require.Equal(t, schema.LegacyProtocolVersion, version)

			var params schema.CallToolRequestParams
			require.NoError(t, json.Unmarshal(request.Params, &params))
			require.NotNil(t, params.Meta.ProgressToken)
			require.Equal(t, schema.LegacyProtocolVersion, params.Meta.IoModelcontextprotocolProtocolVersion)
		})
	}
}

func julyRequestParams(t *testing.T, capabilities schema.ClientCapabilities) json.RawMessage {
	raw, err := json.Marshal(map[string]interface{}{
		"_meta": map[string]interface{}{
			"io.modelcontextprotocol/clientCapabilities": capabilities,
			"io.modelcontextprotocol/protocolVersion":    schema.LatestProtocolVersion,
		},
	})
	require.NoError(t, err)
	return raw
}
