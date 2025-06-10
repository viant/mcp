package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/jsonrpc/transport/client/stdio"
	"github.com/viant/mcp-protocol/schema"
	"testing"
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
		Experimental: make(schema.ClientCapabilitiesExperimental),
		Roots:        &schema.ClientCapabilitiesRoots{},
		Sampling:     make(schema.ClientCapabilitiesSampling),
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

	//clientHandler.CallTool(ctx, &schema.CallToolRequestParams{
	//	Name: "vendor",
	//	Arguments: map[string]any{
	//		"VendorName": "",
	//	},
	//})

}

func getStdioTransport(ctx context.Context) (*stdio.Client, error) {
	transport, err := stdio.New("/tmp/datly",
		stdio.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}),
		stdio.WithArguments("mcp -c /Users/awitas/go/src/github.com/viant/datly/e2e/local/autogen/Datly/config.json -z /tmp/jobs/datly"))
	return transport, err
}

func getHttpTransport(ctx context.Context) (*sse.Client, error) {
	transport, err := sse.New(ctx, "http://localhost:4981/sse",
		sse.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	return transport, err
}
