package term

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/stretchr/testify/assert"
	"github.com/viant/gosh"
	"github.com/viant/gosh/runner/local"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	streamable "github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/example/tool"
	"github.com/viant/mcp/server/auth"

	"github.com/viant/mcp-protocol/schema"

	"net/http"
	"testing"
	"time"

	"net/http/cookiejar"

	"github.com/viant/mcp/client"
	clientauth "github.com/viant/mcp/client/auth"
	"github.com/viant/mcp/client/auth/mock"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/server"
	"github.com/viant/scy/auth/flow"
)

func TestNew(t *testing.T) {
	//t.Skip("Skipping clientauth example experimental tests until OAuth support is refactored")
	authURL, authClose := startAuthorizer(t)
	defer authClose()
	mcpURL, mcpClose := startServer(t, authURL, false)
	defer mcpClose()

	err := runClient(t, authURL, mcpURL)
	assert.Nil(t, err)
}

func TestNew_StreamableHTTP(t *testing.T) {
	// Use isolated ports to avoid clashes with SSE test
	authURL, authClose := startAuthorizer(t)
	defer authClose()
	mcpURL, mcpClose := startServer(t, authURL, true)
	defer mcpClose()

	ctx := context.Background()

	// RoundTripper for OAuth and Authorizer interceptor
	// RoundTripper configured for the stream auth issuer
	aStore := store.NewMemoryStore(store.WithClientConfig(mock.NewTestClient(authURL)))
	jar, _ := cookiejar.New(nil)
	rt, err := transport.New(
		transport.WithStore(aStore),
		transport.WithAuthFlow(flow.NewOutOfBandFlow()),
		transport.WithCookieJar(jar),
	)
	if !assert.NoError(t, err) {
		return
	}
	httpClient := &http.Client{Transport: rt, Jar: jar}
	transport, err := streamable.New(ctx, mcpURL+"/mcp",
		streamable.WithHTTPClient(httpClient),
		streamable.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	if !assert.NoError(t, err) {
		return
	}

	authorizer := &clientauth.Authorizer{Transport: rt}
	aClient := client.New("tester", "0.1", transport,
		client.WithCapabilities(schema.ClientCapabilities{}),
		client.WithAuthInterceptor(authorizer))

	initResult, err := aClient.Initialize(ctx)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "MCP Terminal", initResult.ServerInfo.Name)

	cmd, err := schema.NewCallToolRequestParams[*tool.TerminalCommand]("terminal", &tool.TerminalCommand{Commands: []string{"echo hello"}})
	if !assert.NoError(t, err) {
		return
	}
	content, rErr := aClient.CallTool(ctx, cmd)
	if !assert.Nil(t, rErr) {
		return
	}
	assert.NotNil(t, content)

	// Two more calls; reuse persisted auth (cookie or token)
	for i := 0; i < 2; i++ {
		content, rErr = aClient.CallTool(ctx, cmd)
		if !assert.Nil(t, rErr) {
			return
		}
		assert.NotNil(t, content)
	}
}

func startAuthorizer(t *testing.T) (string, func()) {
	t.Helper()
	mockService, err := mock.NewAuthorizationService()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mockService.Issuer = "http://" + ln.Addr().String()
	aServer := http.Server{}
	aServer.Handler = mockService.Handler()
	go func() { _ = aServer.Serve(ln) }()
	waitForTCP(t, ln.Addr().String())
	return mockService.Issuer, func() { _ = aServer.Close() }
}

func startServer(t *testing.T, authURL string, streamableHTTP bool) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mcpURL := "http://" + ln.Addr().String()

	goshService, err := gosh.New(context.TODO(), local.New())
	if err != nil {
		t.Fatal(err)
	}
	terminalTool := tool.NewTool(goshService)
	newServer := serverproto.WithDefaultHandler(context.Background(), func(server *serverproto.DefaultHandler) error {
		serverproto.RegisterTool[*tool.TerminalCommand, *tool.CommandOutput](server.Registry, "terminal", "Run terminal commands", terminalTool.Call)
		return nil
	})

	authService, err := authorizationService(authURL, mcpURL)
	if err != nil {
		t.Fatal(err)
	}

	var options = []server.Option{
		server.WithJRPCAuthorizer(authService.EnsureAuthorized),
		server.WithNewHandler(newServer),
		server.WithImplementation(schema.Implementation{Name: "MCP Terminal", Version: "0.1"}),
	}
	srv, err := server.New(options...)
	if err != nil {
		t.Fatal(err)
	}
	if streamableHTTP {
		srv.UseStreamableHTTP(true)
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ln.Addr().String())
	go func() { _ = endpoint.Serve(ln) }()
	waitForTCP(t, ln.Addr().String())
	return mcpURL, func() { _ = endpoint.Close() }
}

func runClient(t *testing.T, authURL, mcpURL string) error {
	ctx := context.Background()
	aTransport, err := getHttpTransport(ctx, authURL, mcpURL)
	if err != nil {
		return err
	}

	roundTripper, err := getRoundTripper(authURL)
	if err != nil {
		return err
	}
	authorizer := &clientauth.Authorizer{Transport: roundTripper}

	// Create a new aClient
	aClient := client.New("tester", "0.1", aTransport,
		client.WithCapabilities(schema.ClientCapabilities{}),
		client.WithAuthInterceptor(authorizer))

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
	content, rErr := aClient.CallTool(ctx, cmd)
	if !assert.Nil(t, rErr) {
		return jErr
	}
	assert.NotNil(t, content)

	// Two more tool calls after auth is established
	for i := 0; i < 2; i++ {
		content, rErr = aClient.CallTool(ctx, cmd)
		if !assert.Nil(t, rErr) {
			return rErr
		}
		assert.NotNil(t, content)
	}
	return nil
}

func getHttpTransport(ctx context.Context, authURL, mcpURL string) (*sse.Client, error) {
	roundTripper, err := getRoundTripper(authURL)
	httpClient := &http.Client{Transport: roundTripper}

	sseTransport, err := sse.New(ctx, mcpURL+"/sse",
		sse.WithMessageHttpClient(httpClient),
		sse.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	return sseTransport, err
}

func getRoundTripper(authURL string) (*transport.RoundTripper, error) {
	aStore := store.NewMemoryStore(store.WithClientConfig(mock.NewTestClient(authURL)))
	jar, _ := cookiejar.New(nil)
	roundTripper, err := transport.New(
		transport.WithStore(aStore),
		transport.WithAuthFlow(flow.NewOutOfBandFlow()),
		transport.WithCookieJar(jar),
	)
	return roundTripper, err
}

func authorizationService(authURL, resourceURL string) (*auth.Service, error) {
	if resourceURL == "" {
		resourceURL = "http://localhost"
	}
	policy := &authorization.Policy{
		ExcludeURI: "/sse",
		Tools: map[string]*authorization.Authorization{ //tool level
			"terminal": &authorization.Authorization{ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				Resource: resourceURL,
				AuthorizationServers: []string{
					authURL + "/",
				}},
				RequiredScopes: []string{"openid", "profile", "email"},
			},
		},
	}
	return auth.New(&auth.Config{Policy: policy})
}

func waitForTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", addr)
}
