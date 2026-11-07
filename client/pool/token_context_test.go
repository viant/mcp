package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
)

func TestHostTokenPreparationRefreshesOnlyLegacyTransport(t *testing.T) {
	useID := true
	configured := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{UseIdToken: useID}}}
	provider := &authTokenProviderConfigStub{cfg: configured}
	calls := 0
	manager, err := New(provider, WithTokenContextPreparer(func(ctx context.Context, name string, id bool) context.Context {
		calls++
		require.Equal(t, "server", name)
		require.True(t, id)
		return authctx.WithIDToken(ctx, "refreshed-id")
	}))
	require.NoError(t, err)
	source := authctx.WithCanonicalUserID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "subject"}), "owner")
	refreshed := manager.WithAuthTokenContext(source, "server")
	require.Equal(t, "refreshed-id", refreshed.Value(authtransport.ContextAuthTokenKey))
	require.Equal(t, "subject", authctx.EffectiveUserID(refreshed))
	require.Equal(t, "owner", authctx.CanonicalUserID(refreshed))
	require.Empty(t, authctx.IDToken(source), "preparation must not mutate the incoming context")
	require.Equal(t, 1, calls)
	configured.Auth = &mcp.ClientAuth{Mode: "oauth", ProviderRef: "delegated"}
	delegated := manager.WithAuthTokenContext(source, "server")
	require.Equal(t, source, delegated)
	require.Equal(t, 1, calls, "delegated transport must continue to use only its credential resolver")
}

func TestHostTokenPreparationDoesNotRunForLocalMCP(t *testing.T) {
	manager, err := New(nil, WithTokenContextPreparer(func(context.Context, string, bool) context.Context {
		t.Fatal("local MCP invoked transport token preparation")
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, manager.RegisterLocal(context.Background(), "local", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "local-owner"}, func(context.Context) (mcpclient.Interface, error) { return &stubClient{}, nil }))
	original := context.Background()
	require.Equal(t, original, manager.WithAuthTokenContext(original, "local"))
}
