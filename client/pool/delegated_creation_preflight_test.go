package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	authcfg "github.com/viant/mcp/client/auth/config"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
	mcpserver "github.com/viant/mcp/server"
)

type creationOptionsProvider struct{ options *cfg.MCPClient }

func (p *creationOptionsProvider) Options(context.Context, string) (*cfg.MCPClient, error) {
	raw, err := json.Marshal(p.options)
	if err != nil {
		return nil, err
	}
	var value cfg.MCPClient
	err = json.Unmarshal(raw, &value)
	return &value, err
}

type creationCredentialResolver struct {
	staticCredentialResolver
	mu     sync.Mutex
	linked map[string]bool
}

func (r *creationCredentialResolver) Resolve(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	user := authctx.EffectiveUserID(ctx)
	r.mu.Lock()
	linked := r.linked[user]
	r.mu.Unlock()
	if !linked {
		return nil, authcfg.NewLinkRequired(&requirement, errors.New("user has no active link"))
	}
	return &authcfg.Credential{Token: "delegated-" + user, TokenType: requirement.TokenType, ProviderRef: requirement.ProviderRef, Resource: requirement.Resource, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func creationDelegatedOptions(endpoint string) *cfg.MCPClient {
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "delegated", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint}}, Auth: &mcp.ClientAuth{Mode: authcfg.ModeOAuth, ProviderRef: "approved-provider", ClientRef: "web", Resource: endpoint, TokenType: string(authcfg.TokenTypeAccessToken)}}}
}
func creationUser(name string) context.Context {
	return authctx.WithIDToken(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: name}), "workspace-token-must-not-be-forwarded")
}

func TestManagerGetUnlinkedDelegatedSkipsDiscoveryAndTransport(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "must not contact unlinked source", 500)
	}))
	defer peer.Close()
	// Trust the local test origin so a premature metadata probe is observable.
	original := http.DefaultTransport
	http.DefaultTransport = peer.Client().Transport
	defer func() { http.DefaultTransport = original }()
	options := creationDelegatedOptions(peer.URL + "/mcp")
	options.ProtectedResourceMetadataURL = peer.URL + "/.well-known/oauth-protected-resource"
	registry, err := authcfg.NewStaticRegistry(testBindingProvider())
	require.NoError(t, err)
	resolver := &creationCredentialResolver{linked: map[string]bool{}}
	mapped := errors.New("host presentation of required link")
	mgr, err := New(&creationOptionsProvider{options}, WithProviderRegistry(registry), WithCredentialResolver(resolver), WithCredentialErrorHandler(func(_ context.Context, _ *cfg.MCPClient, _ authcfg.Requirement, err error) error {
		var required *authcfg.OAuthLinkRequiredError
		if errors.As(err, &required) {
			return mapped
		}
		return err
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("session")
	for range 2 {
		_, err = mgr.Get(creationUser("alice"), "session", "delegated")
		require.ErrorIs(t, err, mapped, "preserve the existing host credential error mapping")
	}
	require.Zero(t, requests.Load(), "unlinked sources must not receive metadata, initialize, or list requests")
}

func TestManagerGetDelegatedPreflightRetainsUserAndConversationIsolation(t *testing.T) {
	server, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(_ context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocol.Handler, error) {
		handler := protocol.NewDefaultHandler(n, l, c)
		handler.Registry.RegisterToolWithSchema("echo", "Echo", schema.ToolInputSchema{Type: "object"}, nil, func(context.Context, *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			return &schema.CallToolResult{}, nil
		})
		return handler, nil
	}))
	require.NoError(t, err)
	server.UseStreamableHTTP(true)
	handler := server.HTTP(context.Background(), "").Handler
	var requests atomic.Int32
	var mu sync.Mutex
	var tokens []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodPost {
			mu.Lock()
			tokens = append(tokens, r.Header.Get("Authorization"))
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	defer peer.Close()
	registry, err := authcfg.NewStaticRegistry(testBindingProvider())
	require.NoError(t, err)
	resolver := &creationCredentialResolver{linked: map[string]bool{"alice": true}}
	mgr, err := New(&creationOptionsProvider{creationDelegatedOptions(peer.URL + "/mcp")}, WithProviderRegistry(registry), WithCredentialResolver(resolver))
	require.NoError(t, err)
	defer mgr.CloseConversation("one")
	defer mgr.CloseConversation("two")
	alice := creationUser("alice")
	bob := creationUser("bob")
	first, err := mgr.Get(alice, "one", "delegated")
	require.NoError(t, err)
	same, err := mgr.Get(alice, "one", "delegated")
	require.NoError(t, err)
	require.Same(t, first, same)
	second, err := mgr.Get(alice, "two", "delegated")
	require.NoError(t, err)
	require.NotSame(t, first, second)
	_, err = first.ListTools(alice, nil)
	require.NoError(t, err)
	before := requests.Load()
	_, err = mgr.Get(bob, "one", "delegated")
	var required *authcfg.OAuthLinkRequiredError
	require.ErrorAs(t, err, &required)
	require.Equal(t, before, requests.Load(), "another user's active connection must not hide the missing link")
	resolver.mu.Lock()
	resolver.linked["bob"] = true
	resolver.mu.Unlock()
	third, err := mgr.Get(bob, "one", "delegated")
	require.NoError(t, err)
	require.NotSame(t, first, third)
	_, err = third.ListTools(bob, nil)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, tokens, "Bearer delegated-alice")
	require.Contains(t, tokens, "Bearer delegated-bob")
	for _, token := range tokens {
		require.NotContains(t, token, "workspace-token", fmt.Sprintf("workspace auth leaked: %q", token))
	}
}
