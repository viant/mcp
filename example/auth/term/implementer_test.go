package term

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/viant/gosh"
	"github.com/viant/gosh/runner/local"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/protocol/client/auth/flow"
	"github.com/viant/mcp/protocol/client/auth/meta"
	"github.com/viant/mcp/protocol/client/auth/mock"
	"github.com/viant/mcp/protocol/client/auth/store"
	"github.com/viant/mcp/protocol/client/auth/transport"
	"github.com/viant/mcp/protocol/server"
	"github.com/viant/mcp/protocol/server/auth"
	"github.com/viant/mcp/schema"
	"net/http"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	go func() {
		err := startServer()
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		err := startAuthorizer()
		if err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(time.Second)
	err := runClient(t)
	assert.Nil(t, err)
}

func runClient(t *testing.T) error {
	ctx := context.Background()
	aTransport, err := getHttpTransport(ctx)
	if err != nil {
		return err
	}
	// Create a new aClient
	aClient := client.New("tester", "0.1", aTransport, client.WithCapabilities(schema.ClientCapabilities{}))
	initResult, err := aClient.Initialize(ctx)
	if err != nil {
		return err
	}
	assert.Equal(t, "MCP Terminal", initResult.ServerInfo.Name)
	listResult, jErr := aClient.ListTools(ctx, nil)
	if !assert.Nil(t, jErr) {
		return jErr
	}
	assert.Equal(t, 1, len(listResult.Tools))

	cmd, err := schema.NewCallToolRequestParams[TerminalCommand]("terminal", &TerminalCommand{
		Commands: []string{"echo hello"},
	})
	if !assert.Nil(t, err) {
		return err
	}
	content, rErr := aClient.CallTool(ctx, cmd)
	if !assert.Nil(t, rErr) {
		return jErr
	}
	assert.NotNil(t, content)
	return nil
}

func startAuthorizer() error {
	mockService, err := mock.NewAuthorizationService()
	if err != nil {
		return err
	}
	mockService.Issuer = "http://localhost:8089"
	aServer := http.Server{}
	aServer.Addr = ":8089"
	aServer.Handler = mockService.Handler()
	return aServer.ListenAndServe()

}

func startServer() error {

	goshService, err := gosh.New(context.TODO(), local.New())
	if err != nil {
		return err
	}
	var newImplementer = New(goshService)
	var options = []server.Option{
		server.WithAuthConfig(&auth.Config{
			ExcludeURI: "/sse",
			Global: &meta.ProtectedResourceMetadata{
				Resource: "http://localhost:4981",
				AuthorizationServers: []string{
					"http://localhost:8089/",
				},
			},
		}),
		server.WithNewImplementer(newImplementer),
		server.WithImplementation(schema.Implementation{"MCP Terminal", "0.1"}),
		server.WithCapabilities(schema.ServerCapabilities{
			Resources: &schema.ServerCapabilitiesResources{},
		}),
	}
	srv, err := server.New(options...)
	if err != nil {
		return err
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ":4981")
	return endpoint.ListenAndServe()
}

func getHttpTransport(ctx context.Context) (*sse.Client, error) {
	aStore := store.NewMemoryStore(store.WithClient(mock.NewTestClient("http://localhost:8089")))
	roundTripper, err := transport.New(transport.WithStore(aStore), transport.WithAuthFlow(flow.NewOutOfBandFlow()))
	httpClient := &http.Client{Transport: roundTripper}
	sseTransport, err := sse.New(ctx, "http://localhost:4981/sse",
		sse.WithRPCHTTPClient(httpClient),
		sse.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	return sseTransport, err
}
