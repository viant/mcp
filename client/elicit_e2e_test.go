//go:build transport

package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"net/http"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	sseclient "github.com/viant/jsonrpc/transport/client/http/sse"
	streamingclient "github.com/viant/jsonrpc/transport/client/http/streamable"
	sseserver "github.com/viant/jsonrpc/transport/server/http/sse"
	streamingserver "github.com/viant/jsonrpc/transport/server/http/streamable"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	clientpkg "github.com/viant/mcp/client"
)

// testClientHandler implements pclient.Handler to handle server-initiated calls (elicitation).
type testClientHandler struct {
	last int
}

func (t *testClientHandler) Notify(ctx context.Context, n *jsonrpc.Notification) error { return nil }
func (t *testClientHandler) NextRequestID() jsonrpc.RequestId                          { t.last++; return t.last }
func (t *testClientHandler) LastRequestID() jsonrpc.RequestId                          { return t.last }

// Advertise Elicitation support so the server can call back.
func (t *testClientHandler) Implements(method string) bool {
	return method == schema.MethodElicitationCreate || method == schema.MethodSamplingCreateMessage || method == schema.MethodRootsList
}

func (t *testClientHandler) Init(ctx context.Context, _ *schema.ClientCapabilities) {}

func (t *testClientHandler) OnNotification(ctx context.Context, _ *jsonrpc.Notification) {}

// Elicit returns an accepted result with deterministic content.
func (t *testClientHandler) Elicit(ctx context.Context, req *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	return &schema.ElicitResult{
		Action:  schema.ElicitResultActionAccept,
		Content: map[string]interface{}{"email": "user@example.com", "code": 1234},
	}, nil
}

// Unused in these tests, but required by the interface.
func (t *testClientHandler) ListRoots(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	return &schema.ListRootsResult{}, nil
}
func (t *testClientHandler) CreateMessage(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return &schema.CreateMessageResult{}, nil
}

// Ensure testClientHandler satisfies the pclient.Handler interface.
var _ pclient.Handler = (*testClientHandler)(nil)

// startHTTPServer spins up an MCP HTTP server. If useStreaming is true, the streaming transport is enabled; otherwise SSE.
// rawHandler is a minimal server-side JSON-RPC handler sufficient for this test.
type rawHandler struct {
	tr transport.Transport
}

func (h *rawHandler) Serve(ctx context.Context, req *jsonrpc.Request, resp *jsonrpc.Response) {
	resp.Id = req.Id
	resp.Jsonrpc = req.Jsonrpc
	switch req.Method {
	case schema.MethodInitialize:
		result := &schema.InitializeResult{
			ServerInfo:      schema.Implementation{Name: "TestServer", Version: "1.0"},
			ProtocolVersion: schema.LatestProtocolVersion,
			Capabilities:    schema.ServerCapabilities{Tools: &schema.ServerCapabilitiesTools{}},
		}
		data, _ := json.Marshal(result)
		resp.Result = data
	case schema.MethodToolsList:
		result := &schema.ListToolsResult{Tools: []schema.Tool{{Name: "needsElicit", InputSchema: schema.ToolInputSchema{Type: "object"}}}}
		data, _ := json.Marshal(result)
		resp.Result = data
	case schema.MethodToolsCall:
		// Trigger an elicitation request to the client over the same transport.
		params := schema.ElicitRequestParams{
			ElicitationId: "el1",
			Message:       "Provide email and code",
			Mode:          schema.ElicitRequestParamsModeForm,
			RequestedSchema: schema.ElicitRequestParamsRequestedSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"email": map[string]interface{}{"type": "string"},
					"code":  map[string]interface{}{"type": "number"},
				},
				Required: []string{"email"},
			},
		}

		call, _ := jsonrpc.NewRequest("elicitation/create", &params)
		// Let the server generate a request id if supported
		res, err := h.tr.Send(ctx, call)
		if err != nil {
			resp.Error = jsonrpc.NewInternalError(err.Error(), call.Params)
			return
		}
		var elicitRes schema.ElicitResult
		if uErr := json.Unmarshal(res.Result, &elicitRes); uErr != nil {
			resp.Error = jsonrpc.NewInternalError(uErr.Error(), nil)
			return
		}
		out := &schema.CallToolResult{StructuredContent: map[string]interface{}{"elicited": elicitRes.Content}}
		data, _ := json.Marshal(out)
		resp.Result = data
	default:
		resp.Error = jsonrpc.NewMethodNotFound("not found", nil)
	}
}

func (h *rawHandler) OnNotification(ctx context.Context, _ *jsonrpc.Notification) {}

func startHTTPServer(t *testing.T, useStreaming bool) (baseURL string, shutdown func()) {
	t.Helper()
	// Build HTTP handler with either SSE or streaming transport.
	mux := http.NewServeMux()
	newHandler := func(ctx context.Context, tr transport.Transport) transport.Handler {
		return &rawHandler{tr: tr}
	}
	if useStreaming {
		h := streamingserver.New(newHandler)
		mux.Handle("/mcp", h)
	} else {
		h := sseserver.New(newHandler)
		mux.Handle("/", h)
	}
	httpSrv := &http.Server{Handler: mux}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = httpSrv.Serve(ln) }()

	host := ln.Addr().String()
	base := fmt.Sprintf("http://%s/", host)
	return base, func() {
		//_ = httpSrv.Shutdown(context.Background())
		_ = ln.Close()
		go httpSrv.Shutdown(context.Background())

	}
}

func TestClient_SSE_ToolCallsElicit(t *testing.T) {
	baseURL, stop := startHTTPServer(t, false)
	defer stop()

	// SSE clients connect to "/sse" endpoint.
	ctx := context.Background()
	transport, err := sseclient.New(ctx, baseURL+"sse",
		sseclient.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("client: %v %v %+v\n", string(data), err, message)
		}),
		sseclient.WithHandler(clientpkg.NewHandler(&testClientHandler{})))

	require.NoError(t, err)
	require.NotNil(t, transport)

	cli := clientpkg.New("TestClient", "1.0", transport, clientpkg.WithClientHandler(&testClientHandler{}))

	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	// Verify tool is visible
	tools, err := cli.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, tools)

	// Call the tool; the server will elicit data from the client handler.
	params, err := schema.NewCallToolRequestParams[struct{}]("needsElicit", struct{}{})
	require.NoError(t, err)
	res, err := cli.CallTool(ctx, params)

	require.NoError(t, err)
	require.NotNil(t, res)

	// Validate the elicited content is echoed back by the tool
	elicited, ok := res.StructuredContent["elicited"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "user@example.com", elicited["email"])
	require.Equal(t, float64(1234), elicited["code"]) // JSON numbers unmarshal to float64

}

func TestClient_Streaming_ToolCallsElicit(t *testing.T) {
	baseURL, stop := startHTTPServer(t, true)
	defer stop()

	// Streaming clients use the base URL.
	ctx := context.Background()
	transport, err := streamingclient.New(ctx, baseURL+"mcp",
		streamingclient.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("client: %v %v %+v\n", string(data), err, message)
		}),
		streamingclient.WithHandler(clientpkg.NewHandler(&testClientHandler{})))
	require.NoError(t, err)
	require.NotNil(t, transport)

	cli := clientpkg.New("TestClient", "1.0", transport, clientpkg.WithClientHandler(&testClientHandler{}))

	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	// Call the tool; the server will elicit data from the client handler.
	params, err := schema.NewCallToolRequestParams[struct{}]("needsElicit", struct{}{})
	require.NoError(t, err)
	res, err := cli.CallTool(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, res)

	elicited, ok := res.StructuredContent["elicited"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "user@example.com", elicited["email"])
	require.Equal(t, float64(1234), elicited["code"]) // JSON numbers unmarshal to float64
}
