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
