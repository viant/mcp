package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
	mcpserver "github.com/viant/mcp/server"
)

func TestBareAuthDefinitionDoesNotInheritUserTokenOnWire(t *testing.T) {
	server, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocol.Handler, error) {
		h := protocol.NewDefaultHandler(n, l, c)
		h.Registry.RegisterToolWithSchema("echo", "Echo", schema.ToolInputSchema{Type: "object"}, nil, func(_ context.Context, r *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			return &schema.CallToolResult{StructuredContent: r.Params.Arguments}, nil
		})
		return h, nil
	}))
	require.NoError(t, err)
	server.UseStreamableHTTP(true)
	handler := server.HTTP(context.Background(), "").Handler
	var mu sync.Mutex
	var headers []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer peer.Close()
	// This is the authored JSON exported by a bare MCP definition. An omitted
	// auth block has legacy defaults; explicit false values must survive loading.
	var options cfg.MCPClient
	require.NoError(t, json.Unmarshal([]byte(`{"name":"public","auth":{"backendForFrontend":false,"passUserToken":false}}`), &options))
	options.Transport = mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: peer.URL + "/mcp"}}
	options.ProtocolVersion = schema.LatestProtocolVersion
	rt, err := authtransport.New(authtransport.WithTransport(http.DefaultTransport))
	require.NoError(t, err)
	mgr, err := New(&authTokenProviderConfigStub{cfg: &options}, WithAuthRoundTripper(rt))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	ctx, cancel := context.WithTimeout(authctx.WithIDToken(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "fixture-user-token"), 5*time.Second)
	defer cancel()
	client, err := mgr.Get(ctx, "", "public")
	require.NoError(t, err)
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	args := map[string]any{"value": "unchanged"}
	result, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "echo", Arguments: args})
	require.NoError(t, err)
	require.Equal(t, args, result.StructuredContent)
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(headers), 3)
	for _, header := range headers {
		require.Empty(t, header, "bare source inherited user authentication")
	}
}
