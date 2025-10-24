package client

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/stdio"
	"github.com/viant/mcp-protocol/schema"
	schema2 "github.com/viant/mcp-protocol/schema/2025-06-18"
)

func TestClient(t *testing.T) {
	//t.Skip("Skipping stdio clientHandler tests after protocol refactor")
	ctx := context.Background()
	//transport, err := getStdioTransport(ctx)
	transport, err := getHttpTransport(ctx)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
		return
	}

	// Create a new clientHandler
	client := New("datly", "0.1", transport, WithCapabilities(schema.ClientCapabilities{
		Experimental: make(map[string]map[string]any),
		Roots:        &schema.ClientCapabilitiesRoots{},
		Sampling:     make(map[string]any),
		Elicitation:  map[string]any{},
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

func getHttpTransport(ctx context.Context) (*sse.Client, error) {
	transport, err := sse.New(ctx, "http://localhost:7788/sse",
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
	case methodElicit:
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
