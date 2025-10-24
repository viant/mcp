package percall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	authtransport "github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/example/tool"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/auth"
	"net/http/cookiejar"
)

const (
	perCallAuthPort   = 8091
	perCallServerPort = 4986
)

func Test_PerCallAuth(t *testing.T) {
	go func() {
		if err := startAuthorizer(); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		if err := startServer(); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	err := runClient(t)
	assert.Nil(t, err)
}

func Test_PerCallAuth_StreamableHTTP(t *testing.T) {
	go func() {
		if err := startAuthorizer(); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		if err := startServer(); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(1500 * time.Millisecond)

	ctx := context.Background()
	// streamable HTTP transport with auth RoundTripper (per-call token via options)
	tr, err := getAuthHTTPClient()
	if !assert.NoError(t, err) {
		return
	}

	transport, err := streamable.New(ctx, fmt.Sprintf("http://localhost:%d/mcp", perCallServerPort), streamable.WithHTTPClient(tr))
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
	token, err := obtainAccessToken(fmt.Sprintf("http://localhost:%d", perCallAuthPort))
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

func runClient(t *testing.T) error {
	ctx := context.Background()
	// Plain HTTP client; auth is supplied per call via options
	tr, err := getHttpTransport(ctx)
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
	token, err := obtainAccessToken(fmt.Sprintf("http://localhost:%d", perCallAuthPort))
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

func startAuthorizer() error {
	svc, err := mock.NewAuthorizationService()
	if err != nil {
		return err
	}
	svc.Issuer = fmt.Sprintf("http://localhost:%d", perCallAuthPort)
	httpServer := &http.Server{Addr: fmt.Sprintf("localhost:%d", perCallAuthPort), Handler: svc.Handler()}
	return httpServer.ListenAndServe()
}

func startServer() error {
	goshService, err := gosh.New(context.TODO(), local.New())
	if err != nil {
		return err
	}
	terminalTool := tool.NewTool(goshService)
	newHandler := serverproto.WithDefaultHandler(context.Background(), func(h *serverproto.DefaultHandler) error {
		serverproto.RegisterTool[*tool.TerminalCommand, *tool.CommandOutput](h.Registry, "terminal", "Run terminal commands", terminalTool.Call)
		return nil
	})

	authSvc, err := authorizationService()
	if err != nil {
		return err
	}

	opts := []server.Option{
		server.WithAuthorizer(authSvc.Middleware),
		server.WithProtectedResourcesHandler(authSvc.ProtectedResourcesHandler),
		server.WithNewHandler(newHandler),
		server.WithImplementation(schema.Implementation{Name: "MCP Terminal", Version: "0.1"}),
	}
	srv, err := server.New(opts...)
	if err != nil {
		return err
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, fmt.Sprintf(":%d", perCallServerPort))
	return endpoint.ListenAndServe()
}

func getHttpTransport(ctx context.Context) (*sse.Client, error) {
	// Use auth RoundTripper so Authorization header can be injected from context per call.
	rt, _ := authtransport.New()
	httpClient := &http.Client{Transport: rt}
	return sse.New(ctx, fmt.Sprintf("http://localhost:%d/sse", perCallServerPort),
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

func authorizationService() (*auth.Service, error) {
	policy := &authorization.Policy{
		ExcludeURI: "/sse",
		Tools: map[string]*authorization.Authorization{
			"terminal": {ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
				Resource:             fmt.Sprintf("http://localhost:%d", perCallServerPort),
				AuthorizationServers: []string{fmt.Sprintf("http://localhost:%d/", perCallAuthPort)},
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
