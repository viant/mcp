package term

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/gosh"
	"github.com/viant/gosh/runner/local"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	streamable "github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/client/auth/mock"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/example/tool"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/auth"
	"github.com/viant/scy/auth/flow"
	"net/http/cookiejar"
)

func TestNew(t *testing.T) {
	go func() {
		err := startAuthorizer()
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		err := startServer()
		if err != nil {
			t.Error(err)
		}
	}()

	time.Sleep(2 * time.Second)
	err := runClient(t)
	assert.Nil(t, err)
}

func TestNew_StreamableHTTP(t *testing.T) {
	// Use isolated ports to avoid clashes with SSE test
	go func() {
		if err := startAuthorizerStream(); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		if err := startServerStream(); err != nil {
			t.Error(err)
		}
	}()

	time.Sleep(2 * time.Second)
	ctx := context.Background()

	// auth RoundTripper with browser flow
	store := store.NewMemoryStore(store.WithClientConfig(mock.NewTestClient("http://localhost:8096")))
	jar, _ := cookiejar.New(nil)
	rt, err := transport.New(
		transport.WithStore(store),
		transport.WithAuthFlow(flow.NewOutOfBandFlow()),
		transport.WithCookieJar(jar),
	)
	if !assert.NoError(t, err) {
		return
	}

	httpClient := &http.Client{Transport: rt, Jar: jar}
	tr, err := streamable.New(ctx, "http://localhost:4989/mcp",
		streamable.WithHTTPClient(httpClient),
		streamable.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	if !assert.NoError(t, err) {
		return
	}

	cli := client.New("tester", "0.1", tr, client.WithCapabilities(schema.ClientCapabilities{}))
	initResult, err := cli.Initialize(ctx)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "MCP Terminal", initResult.ServerInfo.Name)
	cmd, err := schema.NewCallToolRequestParams[*tool.TerminalCommand]("terminal", &tool.TerminalCommand{Commands: []string{"echo hello"}})
	if !assert.NoError(t, err) {
		return
	}
	content, rErr := cli.CallTool(ctx, cmd, client.WithJsonRpcRequestId(12))
	if !assert.Nil(t, rErr) {
		return
	}
	assert.NotNil(t, content)

	// Make two more calls; cookies and/or bearer should be reused seamlessly
	for i := 0; i < 2; i++ {
		content, rErr = cli.CallTool(ctx, cmd)
		if !assert.Nil(t, rErr) {
			return
		}
		assert.NotNil(t, content)
	}
}

// Streamable-specific authorizer and server on separate ports
func startAuthorizerStream() error {
	mockService, err := mock.NewAuthorizationService()
	if err != nil {
		return err
	}
	mockService.Issuer = "http://localhost:8096"
	aServer := http.Server{}
	aServer.Addr = "localhost:8096"
	aServer.Handler = mockService.Handler()
	return aServer.ListenAndServe()
}

func startServerStream() error {
	goshService, err := gosh.New(context.TODO(), local.New())
	if err != nil {
		return err
	}
	terminalTool := tool.NewTool(goshService)
	newServer := serverproto.WithDefaultHandler(context.Background(), func(server *serverproto.DefaultHandler) error {
		serverproto.RegisterTool[*tool.TerminalCommand, *tool.CommandOutput](server.Registry, "terminal", "Run terminal commands", terminalTool.Call)
		return err
	})

	authService, err := authorizationServiceStream()
	if err != nil {
		return err
	}
	var options = []server.Option{
		server.WithAuthorizer(authService.Middleware),
		server.WithProtectedResourcesHandler(authService.ProtectedResourcesHandler),
		server.WithNewHandler(newServer),
		server.WithImplementation(schema.Implementation{Name: "MCP Terminal", Version: "0.1"}),
	}
	srv, err := server.New(options...)
	if err != nil {
		return err
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ":4989")
	return endpoint.ListenAndServe()
}

func authorizationServiceStream() (*auth.Service, error) {
	policy := &authorization.Policy{
		ExcludeURI: "/sse",
		Tools: map[string]*authorization.Authorization{ //tool level
			"terminal": &authorization.Authorization{ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				Resource: "http://localhost:4989",
				AuthorizationServers: []string{
					"http://localhost:8096/",
				}},
				RequiredScopes: []string{"openid", "profile", "email"},
			},
		},
	}
	return auth.New(&auth.Config{Policy: policy})
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

	cmd, err := schema.NewCallToolRequestParams[*tool.TerminalCommand]("terminal", &tool.TerminalCommand{
		Commands: []string{"echo hello"},
	})
	if !assert.Nil(t, err) {
		return err
	}
	content, rErr := aClient.CallTool(ctx, cmd, client.WithJsonRpcRequestId(11))
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
	aServer.Addr = "localhost:8089"
	aServer.Handler = mockService.Handler()
	return aServer.ListenAndServe()

}

func startServer() error {

	goshService, err := gosh.New(context.TODO(), local.New())
	if err != nil {
		return err
	}
	terminalTool := tool.NewTool(goshService)
	NewServer := serverproto.WithDefaultHandler(context.Background(), func(server *serverproto.DefaultHandler) error {
		serverproto.RegisterTool[*tool.TerminalCommand, *tool.CommandOutput](server.Registry, "terminal", "Run terminal commands", terminalTool.Call)
		return err
	})

	authService, err := authorizationService()
	if err != nil {
		return err
	}
	var options = []server.Option{
		server.WithAuthorizer(authService.Middleware),
		server.WithProtectedResourcesHandler(authService.ProtectedResourcesHandler),
		server.WithNewHandler(NewServer),
		server.WithImplementation(schema.Implementation{Name: "MCP Terminal", Version: "0.1"}),
	}
	srv, err := server.New(options...)

	if err != nil {
		return err
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ":4984")
	return endpoint.ListenAndServe()
}

func getHttpTransport(ctx context.Context) (*sse.Client, error) {

	aStore := store.NewMemoryStore(store.WithClientConfig(mock.NewTestClient("http://localhost:8089")))
	roundTripper, err := transport.New(transport.WithStore(aStore), transport.WithAuthFlow(flow.NewOutOfBandFlow()))
	httpClient := &http.Client{Transport: roundTripper}

	sseTransport, err := sse.New(ctx, "http://localhost:4984/sse",
		sse.WithMessageHttpClient(httpClient),
		sse.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	return sseTransport, err
}

func authorizationService() (*auth.Service, error) {
	policy := &authorization.Policy{
		ExcludeURI: "/sse",
		Tools: map[string]*authorization.Authorization{ //tool level
			"terminal": &authorization.Authorization{ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				Resource: "http://localhost:4984",
				AuthorizationServers: []string{
					"http://localhost:8089/",
				}},
				RequiredScopes: []string{"openid", "profile", "email"},
			},
		},
	}
	return auth.New(&auth.Config{Policy: policy})
}
