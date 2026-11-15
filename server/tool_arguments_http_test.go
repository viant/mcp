package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
)

func TestHTTPToolArgumentsJSONRetainsStandardLegacyClientContract(t *testing.T) {
	const arguments = `{"definition":{"z":9007199254740993,"a":{"y":1,"b":2}},"enabled":false}`
	var calls atomic.Int64
	server, err := New(WithNewHandler(protocolserver.WithDefaultHandler(context.Background(), func(handler *protocolserver.DefaultHandler) error {
		handler.Registry.RegisterToolWithSchema("example", "Example", schema.ToolInputSchema{Type: "object"}, nil, func(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			calls.Add(1)
			raw, ok := ToolArgumentsJSON(ctx, request)
			require.True(t, ok)
			require.Equal(t, arguments, string(raw))
			require.Equal(t, false, request.Params.Arguments["enabled"])
			require.Equal(t, float64(9007199254740992), request.Params.Arguments["definition"].(map[string]any)["z"])
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
	_, raw := post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"example","arguments":`+arguments+`}}`, session)
	var response struct {
		Error  *jsonrpc.Error        `json:"error"`
		Result schema.CallToolResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &response))
	require.Nil(t, response.Error)
	require.True(t, response.Result.IsError == nil || !*response.Result.IsError)
	require.Equal(t, int64(1), calls.Load())
}
