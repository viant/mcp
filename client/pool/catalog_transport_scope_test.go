package manager

import (
	"context"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	transport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
)

func TestCatalogUnusedAuthHookDoesNotDisablePlainHTTPMetadata(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	called := false
	mgr, err := New(nil, WithCookieJar(jar), WithAuthRoundTripperProvider(func(context.Context) *transport.RoundTripper { called = true; return nil }))
	require.NoError(t, err)
	scope, cacheable, err := mgr.catalogTransportCredentialScope(context.Background(), "plain", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "http://example.test/mcp"}}}})
	require.NoError(t, err)
	require.True(t, cacheable)
	require.NotEmpty(t, scope)
	require.False(t, called, "auth:null does not use the configured BFF hook")
}
