package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
)

type rawArgumentsHandler struct {
	protocolserver.Handler
	call func(context.Context, *schema.CallToolRequest)
}

func (h *rawArgumentsHandler) CallTool(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	h.call(ctx, request.Request)
	return &schema.CallToolResult{}, nil
}

func TestToolArgumentsJSONPreservesWireAndLegacyDecodedArguments(t *testing.T) {
	const arguments = `{ "definition": {"z":9007199254740993,"a":"<sample>&"}, "enabled":false }`
	calls := 0
	h := &Handler{handler: &rawArgumentsHandler{call: func(ctx context.Context, request *schema.CallToolRequest) {
		calls++
		raw, ok := ToolArgumentsJSON(ctx, request)
		require.True(t, ok)
		require.Equal(t, arguments, string(raw))
		require.Equal(t, float64(9007199254740992), request.Params.Arguments["definition"].(map[string]any)["z"], "existing decoded argument types stay unchanged")
		raw[0] = '['
		retained, _ := ToolArgumentsJSON(ctx, request)
		require.Equal(t, arguments, string(retained), "callers cannot mutate the retained request")
		_, ok = ToolArgumentsJSON(ctx, &schema.CallToolRequest{})
		require.False(t, ok, "raw arguments belong to one exact invocation")
	}}}
	_, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(`{"_meta":null,"name":"example","arguments":` + arguments + `}`)})
	require.Nil(t, err)
	require.Equal(t, 1, calls)
	for _, raw := range []string{`{"_meta":null,"name":"example","arguments":[]}`, `{"_meta":null,"name":"example","arguments":{"bad":01}}`, `{"_meta":null,"name":"example"} {}`} {
		_, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(raw)})
		require.NotNil(t, err)
	}
	require.Equal(t, 1, calls, "normal decoding still rejects malformed requests before dispatch")
	_, ok := ToolArgumentsJSON(context.Background(), &schema.CallToolRequest{})
	require.False(t, ok)
}

func TestToolArgumentsJSONDistinguishesMissingNullAndEmpty(t *testing.T) {
	for _, arguments := range []string{"", "null", "{}"} {
		t.Run(arguments, func(t *testing.T) {
			h := &Handler{handler: &rawArgumentsHandler{call: func(ctx context.Context, request *schema.CallToolRequest) {
				raw, ok := ToolArgumentsJSON(ctx, request)
				require.True(t, ok)
				require.Equal(t, arguments, string(raw))
				require.Empty(t, request.Params.Arguments)
			}}}
			body := `{"_meta":null,"name":"example"}`
			if arguments != "" {
				body = `{"_meta":null,"name":"example","arguments":` + arguments + `}`
			}
			_, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(body)})
			require.Nil(t, err)
		})
	}
}
