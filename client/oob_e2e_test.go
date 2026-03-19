//go:build transport

package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	sseclient "github.com/viant/jsonrpc/transport/client/http/sse"
	streamingclient "github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	clientpkg "github.com/viant/mcp/client"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/namespace"
	"github.com/viant/mcp/server/oob"
)

type oobData struct {
	Message string
}

func TestOOBForm_SSE(t *testing.T) {
	baseURL, stop := startOOBServer(t, false)
	defer stop()

	ctx := context.Background()
	transport, err := sseclient.New(ctx, baseURL+"/sse",
		sseclient.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("client: %v %v %+v\n", string(data), err, message)
		}))
	require.NoError(t, err)

	cli := clientpkg.New("TestClient", "1.0", transport)
	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	checkOOBFlow(t, ctx, cli)
}

func TestOOBForm_Streaming(t *testing.T) {
	baseURL, stop := startOOBServer(t, true)
	defer stop()

	ctx := context.Background()
	transport, err := streamingclient.New(ctx, baseURL+"/mcp",
		streamingclient.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("client: %v %v %+v\n", string(data), err, message)
		}))
	require.NoError(t, err)

	cli := clientpkg.New("TestClient", "1.0", transport)
	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	checkOOBFlow(t, ctx, cli)
}

func checkOOBFlow(t *testing.T, ctx context.Context, cli *clientpkg.Client) {
	t.Helper()
	params, err := schema.NewCallToolRequestParams[struct{}]("needs-oob", struct{}{})
	require.NoError(t, err)
	res, err := cli.CallTool(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, res.StructuredContent)

	rawURL, ok := res.StructuredContent["oobUrl"].(string)
	require.True(t, ok)
	require.NotEmpty(t, rawURL)

	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "namespace=default")
	require.Contains(t, string(body), "message=Provide credentials")
}

func startOOBServer(t *testing.T, useStreaming bool) (string, func()) {
	t.Helper()
	store := oob.NewMemoryStore[oobData]()
	provider := namespace.NewProvider(nil)
	var baseURL string
	mgr := &oob.Manager[oobData]{
		Provider: provider,
		Store:    store,
		CallbackBuilder: func(id string) string {
			return strings.TrimRight(baseURL, "/") + "/oob?id=" + id
		},
	}

	newHandler := serverproto.WithDefaultHandler(context.Background(), func(h *serverproto.DefaultHandler) error {
		return serverproto.RegisterTool[*struct{}, *struct{}](h.Registry, "needs-oob", "Trigger OOB form", func(ctx context.Context, _ *struct{}) (*schema.CallToolResult, *jsonrpc.Error) {
			_, url, err := mgr.Create(ctx, oob.Spec[oobData]{Kind: "form", Alias: "demo", Resource: "example", Data: oobData{Message: "Provide credentials"}})
			if err != nil {
				return nil, jsonrpc.NewInternalError(err.Error(), nil)
			}
			return &schema.CallToolResult{
				StructuredContent: map[string]interface{}{"oobUrl": url},
				Content: []schema.CallToolResultContentElem{
					schema.TextContent{Text: url, Type: "text"},
				},
			}, nil
		})
	})

	oobHandler := oob.NamespaceFromPending(store, func(r *http.Request) (string, error) {
		return r.URL.Query().Get("id"), nil
	}, func(ctx context.Context, p oob.Pending[oobData], w http.ResponseWriter, _ *http.Request) error {
		desc, _ := namespace.FromContext(ctx)
		_, _ = fmt.Fprintf(w, "namespace=%s; message=%s", desc.Name, p.Data.Message)
		return nil
	})

	srv, err := server.New(
		server.WithNewHandler(newHandler),
		server.WithCustomHTTPHandler("/oob", oobHandler.ServeHTTP),
		server.WithImplementation(schema.Implementation{Name: "oob-test", Version: "0.1"}),
	)
	require.NoError(t, err)
	if useStreaming {
		srv.UseStreamableHTTP(true)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	baseURL = "http://" + ln.Addr().String()
	httpSrv := srv.HTTP(context.Background(), ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	return baseURL, func() { _ = httpSrv.Close() }
}
