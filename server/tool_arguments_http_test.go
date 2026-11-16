package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
)

func TestHTTPToolArgumentsJSONRetainsStandardLegacyClientContract(t *testing.T) {
	const arguments = `{ "definition": {"z":9007199254740993, "a":"<sample>&"}, "enabled":false }`
	type observation struct {
		raw      json.RawMessage
		retained bool
		decoded  map[string]any
	}
	observed := make(chan observation, 1)
	server, err := New(WithNewHandler(protocolserver.WithDefaultHandler(context.Background(), func(handler *protocolserver.DefaultHandler) error {
		handler.Registry.RegisterToolWithSchema("example", "Example", schema.ToolInputSchema{Type: "object"}, nil, func(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			raw, retained := ToolArgumentsJSON(ctx, request)
			observed <- observation{raw: raw, retained: retained, decoded: request.Params.Arguments}
			return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: "ok"}}}, nil
		})
		return nil
	})))
	require.NoError(t, err)
	endpoint := httptest.NewServer(server.HTTP(context.Background(), "127.0.0.1:0").Handler)
	t.Cleanup(endpoint.Close)
	post := func(body, session string) (string, []byte) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, endpoint.URL+"/mcp", bytes.NewBufferString(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, string(raw))
		return response.Header.Get("Mcp-Session-Id"), raw
	}
	session, _ := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-example","version":"1"}}}`, "")
	require.NotEmpty(t, session)
	params := func(member string) string { return `"params":{"name":"example",` + member + `}` }
	canonical := `"arguments":` + arguments
	cases := []struct{ name, members, want string }{
		{"canonical", params(canonical), arguments},
		{"legacy case variant", params(`"Arguments":` + arguments), arguments},
		{"repeated member during legacy normalization", params(`"arguments":{"older":true},` + canonical), arguments},
		{"repeated JSON-RPC params", params(`"arguments":{"older":true}`) + "," + params(canonical), arguments},
		{"case variants inside arguments", params(`"arguments":{"uri":"first","URI":"second"}`), `{"uri":"first","URI":"second"}`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, raw := post(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call",%s}`, i+2, tc.members), session)
			var response struct {
				Error  *jsonrpc.Error        `json:"error"`
				Result schema.CallToolResult `json:"result"`
			}
			require.NoError(t, json.Unmarshal(raw, &response))
			require.Nil(t, response.Error)
			require.True(t, response.Result.IsError == nil || !*response.Result.IsError)
			got := <-observed
			require.True(t, got.retained)
			require.Equal(t, tc.want, string(got.raw))
			var want map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.want), &want))
			require.Equal(t, want, got.decoded)
		})
	}
}
