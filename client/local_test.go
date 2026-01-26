package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/stdio"
	"github.com/viant/mcp-protocol/schema"
	schema2 "github.com/viant/mcp-protocol/schema/2025-06-18"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/server"
)

func TestClient(t *testing.T) {
	ctx := context.Background()
	addr, shutdown := startTestServer(t, ctx)
	defer shutdown()

	transport, err := getHttpTransport(ctx, "http://"+addr+"/sse")
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create a new clientHandler
	client := client.New("datly", "0.1", transport, client.WithCapabilities(schema.ClientCapabilities{
		Experimental: make(map[string]map[string]any),
		Roots:        &schema.ClientCapabilitiesRoots{},
		Sampling:     &schema.ClientCapabilitiesSampling{},
		Elicitation:  &schema.ClientCapabilitiesElicitation{},
	}))
	result, err := client.Initialize(ctx)

	assert.Nil(t, err)
	assert.NotNil(t, result)

	//templateResources, err := clientHandler.ListResourceTemplates(ctx, nil)
	//assert.Nil(t, err)
	//assert.NotNil(t, templateResources)

	tools, err := client.ListTools(ctx, nil)
	assert.Nil(t, err)
	assert.NotNil(t, tools)

	res, err := client.CallTool(ctx, &schema.CallToolRequestParams{
		Name: "outlookListMail",
		Arguments: map[string]any{
			"Top": 10,
		},
	})

	fmt.Printf("%v, err: %v\n", res, err)

}

func getStdioTransport(ctx context.Context) (*stdio.Client, error) {
	transport, err := stdio.New("/tmp/datly",
		stdio.WithListener(func(message *jsonrpc.Message) {
			data, _ := json.Marshal(message)
			fmt.Printf("data: %v\n", string(data))
		}),
		stdio.WithArguments("mcp -c /Users/awitas/go/src/github.com/viant/datly/e2e/local/autogen/Datly/config.json -z /tmp/jobs/datly"))
	return transport, err
}

func getHttpTransport(ctx context.Context, rawURL string) (*sse.Client, error) {
	transport, err := sse.New(ctx, rawURL,
		sse.WithHandler(&transportHandler{}),
		sse.WithListener(func(message *jsonrpc.Message) {
			data, _ := json.Marshal(message)
			fmt.Printf("%v\n", string(data))
		}))
	return transport, err
}

type transportHandler struct{}

func (h *transportHandler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	switch request.Method {
	case "elicitation/create":
		response.Id = request.Id
		response.Jsonrpc = request.Jsonrpc
		result := &schema2.ElicitResult{
			Action: schema2.ElicitResultActionAccept,
		}
		data, _ := json.Marshal(result)

		response.Result = json.RawMessage(data)
	default:
		data, _ := json.Marshal(jsonrpc.NewMethodNotFound(request.Method, nil))
		response.Result = json.RawMessage(data)
	}
}

func (h *transportHandler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {

}

func startTestServer(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	handler := serverproto.WithDefaultHandler(ctx, func(h *serverproto.DefaultHandler) error {
		type ToolInput struct {
			Top int `json:"Top"`
		}
		type ToolOutput struct {
			Count int `json:"count"`
		}
		return serverproto.RegisterTool[*ToolInput, *ToolOutput](h.Registry, "outlookListMail", "List mail", func(ctx context.Context, input *ToolInput) (*schema.CallToolResult, *jsonrpc.Error) {
			out := &ToolOutput{Count: input.Top}
			data, err := json.Marshal(out)
			if err != nil {
				return nil, jsonrpc.NewInternalError(err.Error(), nil)
			}
			return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{
				schema.TextContent{Text: string(data), Type: "text"},
			}}, nil
		})
	})
	srv, err := server.New(
		server.WithNewHandler(handler),
		server.WithImplementation(schema.Implementation{Name: "test", Version: "0.1"}),
	)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	httpSrv := srv.HTTP(ctx, ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	return ln.Addr().String(), func() {
		_ = httpSrv.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = httpSrv.Shutdown(shutdownCtx)
		cancel()
	}
}
