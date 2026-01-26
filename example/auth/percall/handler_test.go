package percall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"net/http/cookiejar"

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
	authtransport "github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/example/tool"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/auth"
)

func Test_PerCallAuth(t *testing.T) {
	authURL, authClose := startAuthorizer(t)
	defer authClose()
	mcpURL, mcpClose := startServer(t, authURL)
	defer mcpClose()

	err := runClient(t, authURL, mcpURL)
	assert.Nil(t, err)
}

func Test_PerCallAuth_StreamableHTTP(t *testing.T) {
	authURL, authClose := startAuthorizer(t)
	defer authClose()
	mcpURL, mcpClose := startServer(t, authURL)
	defer mcpClose()

	ctx := context.Background()
	// streamable HTTP transport with auth RoundTripper (per-call token via options)
	tr, err := getAuthHTTPClient()
	if !assert.NoError(t, err) {
		return
	}

	transport, err := streamable.New(ctx, mcpURL+"/mcp", streamable.WithHTTPClient(tr))
	if !assert.NoError(t, err) {
		return
	}

	cli := client.New("tester", "0.1", transport, client.WithCapabilities(schema.ClientCapabilities{}))
	initRes, err := cli.Initialize(ctx)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "MCP Terminal", initRes.ServerInfo.Name)

	// Acquire access token and use per-call option
	token, err := obtainAccessToken(authURL)
	if !assert.NoError(t, err) {
		return
	}

	cmd, err := schema.NewCallToolRequestParams[*tool.TerminalCommand]("terminal", &tool.TerminalCommand{Commands: []string{"echo hello"}})
	if !assert.NoError(t, err) {
		return
	}
	res, rerr := cli.CallTool(ctx, cmd, client.WithAuthToken(token))
	if !assert.Nil(t, rerr) {
		return
	}
	assert.NotNil(t, res)

	// Two more calls using the same per-call token
	for i := 0; i < 2; i++ {
		res, rerr = cli.CallTool(ctx, cmd, client.WithAuthToken(token))
		if !assert.Nil(t, rerr) {
			return
		}
		assert.NotNil(t, res)
	}
}

func runClient(t *testing.T, authURL, mcpURL string) error {
	ctx := context.Background()
	// Plain HTTP client; auth is supplied per call via options
	tr, err := getHttpTransport(ctx, mcpURL)
	if err != nil {
		return err
	}

	cli := client.New("tester", "0.1", tr, client.WithCapabilities(schema.ClientCapabilities{}))
	initRes, err := cli.Initialize(ctx)
	if err != nil {
		return err
	}
	assert.Equal(t, "MCP Terminal", initRes.ServerInfo.Name)

	// Acquire an access token from the mock authorization server
	token, err := obtainAccessToken(authURL)
	if !assert.NoError(t, err) {
		return err
	}
	assert.NotEmpty(t, token)

	// Optional: ensure server exposes expected tool
	list, jerr := cli.ListTools(ctx, nil)
	if !assert.Nil(t, jerr) {
		return jerr
	}
	assert.GreaterOrEqual(t, len(list.Tools), 1)

	// Call protected tool with per-call token
	cmd, err := schema.NewCallToolRequestParams[*tool.TerminalCommand]("terminal", &tool.TerminalCommand{Commands: []string{"echo hello"}})
	if !assert.Nil(t, err) {
		return err
	}

	res, rerr := cli.CallTool(ctx, cmd, client.WithAuthToken(token), client.WithJsonRpcRequestId(101))
	if !assert.Nil(t, rerr) {
		return rerr
	}
	assert.NotNil(t, res)
	return nil
}

func startAuthorizer(t *testing.T) (string, func()) {
	t.Helper()
	svc, err := mock.NewAuthorizationService()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	svc.Issuer = "http://" + ln.Addr().String()
	httpServer := &http.Server{Handler: svc.Handler()}
	go func() { _ = httpServer.Serve(ln) }()
	waitForTCP(t, ln.Addr().String())
	return svc.Issuer, func() { _ = httpServer.Close() }
}

func startServer(t *testing.T, authURL string) (string, func()) {
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
	newHandler := serverproto.WithDefaultHandler(context.Background(), func(h *serverproto.DefaultHandler) error {
		serverproto.RegisterTool[*tool.TerminalCommand, *tool.CommandOutput](h.Registry, "terminal", "Run terminal commands", terminalTool.Call)
		return nil
	})

	authSvc, err := authorizationService(authURL, mcpURL)
	if err != nil {
		t.Fatal(err)
	}

	opts := []server.Option{
		server.WithAuthorizer(authSvc.Middleware),
		server.WithProtectedResourcesHandler(authSvc.ProtectedResourcesHandler),
		server.WithNewHandler(newHandler),
		server.WithImplementation(schema.Implementation{Name: "MCP Terminal", Version: "0.1"}),
	}
	srv, err := server.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ln.Addr().String())
	go func() { _ = endpoint.Serve(ln) }()
	waitForTCP(t, ln.Addr().String())
	return mcpURL, func() { _ = endpoint.Close() }
}

func getHttpTransport(ctx context.Context, mcpURL string) (*sse.Client, error) {
	// Use auth RoundTripper so Authorization header can be injected from context per call.
	rt, _ := authtransport.New()
	httpClient := &http.Client{Transport: rt}
	return sse.New(ctx, mcpURL+"/sse",
		sse.WithMessageHttpClient(httpClient),
		sse.WithListener(func(m *jsonrpc.Message) {
			// optional debug: b, _ := json.Marshal(m); fmt.Println(string(b))
		}),
	)
}

// getAuthHTTPClient returns an *http.Client configured with the auth RoundTripper
// so Authorization header can be injected from context per call (for streamable HTTP).
func getAuthHTTPClient() (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	rt, err := authtransport.New(authtransport.WithCookieJar(jar))
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, Jar: jar}, nil
}

func authorizationService(authURL, mcpURL string) (*auth.Service, error) {
	policy := &authorization.Policy{
		ExcludeURI: "/mcp",
		Tools: map[string]*authorization.Authorization{
			"terminal": {ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				Resource:             mcpURL,
				AuthorizationServers: []string{authURL + "/"},
			}, RequiredScopes: []string{"openid", "profile", "email"}},
		},
	}
	return auth.New(&auth.Config{Policy: policy})
}

// obtainAccessToken fetches an access token from the mock OAuth2 server using client credentials
// and authorization_code grant (code value is not validated by the mock).
func obtainAccessToken(issuer string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "dummy")
	req, _ := http.NewRequest(http.MethodPost, issuer+"/token", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test_client_id", "test_client_secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %d: %s", resp.StatusCode, string(body))
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return data.AccessToken, nil
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
