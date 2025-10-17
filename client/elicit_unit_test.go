package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

// --- Unit tests for client-side Elicit handling (no network) ---

type mockPClient struct{}

func (m *mockPClient) Notify(ctx context.Context, n *jsonrpc.Notification) error   { return nil }
func (m *mockPClient) NextRequestID() jsonrpc.RequestId                            { return 1 }
func (m *mockPClient) LastRequestID() jsonrpc.RequestId                            { return 1 }
func (m *mockPClient) Implements(method string) bool                               { return method == schema.MethodElicitationCreate }
func (m *mockPClient) Init(ctx context.Context, _ *schema.ClientCapabilities)      {}
func (m *mockPClient) OnNotification(ctx context.Context, _ *jsonrpc.Notification) {}
func (m *mockPClient) ListRoots(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	return &schema.ListRootsResult{}, nil
}
func (m *mockPClient) CreateMessage(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return &schema.CreateMessageResult{}, nil
}
func (m *mockPClient) Elicit(ctx context.Context, req *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	return &schema.ElicitResult{Action: schema.ElicitResultActionAccept, Content: map[string]any{"k": "v"}}, nil
}

var _ pclient.Handler = (*mockPClient)(nil)

func TestHandler_Elicit_Dispatch(t *testing.T) {
	// Build the client-side handler wrapper
	h := NewHandler(&mockPClient{})
	reqParams := schema.ElicitRequestParams{
		ElicitationId: "e1",
		Message:       "msg",
		Mode:          string(schema.ElicitRequestParamsModeForm),
		RequestedSchema: schema.ElicitRequestParamsRequestedSchema{
			Type:       "object",
			Properties: map[string]any{"k": map[string]any{"type": "string"}},
			Required:   []string{"k"},
		},
	}
	raw, _ := json.Marshal(&reqParams)
	req := &jsonrpc.Request{Jsonrpc: jsonrpc.Version, Method: schema.MethodElicitationCreate, Params: raw, Id: 1}
	resp := &jsonrpc.Response{}
	h.Serve(context.Background(), req, resp)

	require.Nil(t, resp.Error)
	var out schema.ElicitResult
	require.NoError(t, json.Unmarshal(resp.Result, &out))
	require.Equal(t, schema.ElicitResultActionAccept, out.Action)
	require.Equal(t, "v", out.Content["k"])
}

// mock transport to capture send and return a canned response
type mockTransport struct {
	send func(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error)
}

func (m *mockTransport) Notify(ctx context.Context, n *jsonrpc.Notification) error { return nil }
func (m *mockTransport) Send(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
	return m.send(ctx, r)
}

var _ transport.Transport = (*mockTransport)(nil)

func TestClient_Elicit_Send(t *testing.T) {
	// Prepare a transport that asserts method and returns an accept result.
	mt := &mockTransport{send: func(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
		require.Equal(t, "elicitation/create", r.Method)
		accept := &schema.ElicitResult{Action: schema.ElicitResultActionAccept}
		data, _ := json.Marshal(accept)
		return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: data}, nil
	}}

	c := &Client{transport: mt, initialized: true}
	out, err := c.Elicit(context.Background(), &schema.ElicitRequestParams{ElicitationId: "e1", Message: "m", RequestedSchema: schema.ElicitRequestParamsRequestedSchema{Type: "object", Properties: map[string]any{}}})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, schema.ElicitResultActionAccept, out.Action)
}
