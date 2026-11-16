package server

import (
	"bytes"
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

func TestAdapterRawValuesSurviveMetadataNormalization(t *testing.T) {
	const definition = `{ "z": 9007199254740993, "a": "<value>&" }`
	calls := 0
	server, err := New(WithNewHandler(protocolserver.WithDefaultHandler(context.Background(), func(handler *protocolserver.DefaultHandler) error {
		handler.Registry.RegisterToolWithSchema("example", "Example", schema.ToolInputSchema{Type: "object"}, nil, func(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			calls++
			raw, ok := ToolArgumentsJSON(ctx, request)
			require.True(t, ok)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields))
			require.Equal(t, definition, string(fields["definition"]))
			require.Equal(t, definition, string(fields["key.with:syntax"]))
			require.Equal(t, definition, string(fields[""]))
			require.Equal(t, 1, bytes.Count(raw, []byte(`"":`)), "overlay must replace the empty member exactly once")
			require.Equal(t, float64(9007199254740992), request.Params.Arguments["definition"].(map[string]any)["z"])
			return &schema.CallToolResult{}, nil
		})
		return nil
	})))
	require.NoError(t, err)
	client := server.AsClient(context.Background())
	_, err = client.Initialize(context.Background())
	require.NoError(t, err)
	_, err = client.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "example", Arguments: map[string]any{"definition": json.RawMessage(definition), "key.with:syntax": json.RawMessage(definition), "": json.RawMessage(definition)}})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestRepeatedArgumentsDoNotExposeAPartialRawObject(t *testing.T) {
	for _, name := range []string{"arguments", "Arguments"} {
		t.Run(name, func(t *testing.T) {
			called := false
			h := &Handler{handler: &rawArgumentsHandler{call: func(ctx context.Context, req *schema.CallToolRequest) {
				called = true
				require.Equal(t, map[string]any{"scope": "fixture", "id": float64(7)}, req.Params.Arguments)
				raw, ok := ToolArgumentsJSON(ctx, req)
				require.False(t, ok)
				require.Nil(t, raw)
			}}}
			params := `{"_meta":null,"name":"example","arguments":{"scope":"fixture"},"` + name + `":{"id":7}}`
			_, err := h.CallTool(t.Context(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(params)})
			require.Nil(t, err)
			require.True(t, called)
		})
	}
}

func TestNestedDispatchExposesOnlyItsOwnArguments(t *testing.T) {
	outer := &Handler{handler: &rawArgumentsHandler{call: func(ctx context.Context, parent *schema.CallToolRequest) {
		child := &Handler{handler: &rawArgumentsHandler{call: func(childCtx context.Context, req *schema.CallToolRequest) {
			_, ok := ToolArgumentsJSON(childCtx, parent)
			require.False(t, ok, "the nested invocation cannot expose the parent's carrier")
			_, ok = ToolArgumentsJSON(childCtx, req)
			require.False(t, ok, "no single raw value represents the nested merged arguments")
		}}}
		_, err := child.CallTool(ctx, &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(`{"_meta":null,"name":"child","arguments":{"x":1},"Arguments":{"y":2}}`)})
		require.Nil(t, err)
		raw, ok := ToolArgumentsJSON(ctx, parent)
		require.True(t, ok)
		require.JSONEq(t, `{"parent":true}`, string(raw))
	}}}
	_, err := outer.CallTool(t.Context(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(`{"_meta":null,"name":"outer","arguments":{"parent":true}}`)})
	require.Nil(t, err)
}
