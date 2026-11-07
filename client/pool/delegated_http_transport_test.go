package manager

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
)

func delegatedTransportOptions() *mcp.ClientOptions {
	return &mcp.ClientOptions{Name: "fixture", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://example.invalid/mcp"}}, Auth: &mcp.ClientAuth{Mode: "oauth", Resource: "https://example.invalid/mcp", ExternalResolver: staticCredentialResolver{}}}
}
func TestDelegatedHTTPTransportInstalledAfterResolverDecoration(t *testing.T) {
	m := &Manager{}
	options := delegatedTransportOptions()
	wrapped := catalogCredentialResolver{staticCredentialResolver{}}
	options.Auth.ExternalResolver = wrapped
	calls := 0
	require.NoError(t, WithDelegatedHTTPTransport(func(_ context.Context, server, endpoint string) (DelegatedHTTPTransport, error) {
		calls++
		require.Equal(t, "fixture", server)
		require.Equal(t, "https://example.invalid/mcp", endpoint)
		return DelegatedHTTPTransport{Base: http.DefaultTransport, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
	})(m))
	require.NoError(t, m.configureDelegatedHTTPTransport(context.Background(), "fixture", options))
	require.Equal(t, 1, calls)
	require.Equal(t, wrapped, options.Auth.ExternalResolver, "transport policy must preserve credential witness decorator")
}
func TestDelegatedHTTPTransportFailsClosed(t *testing.T) {
	require.Error(t, WithDelegatedHTTPTransport(nil)(&Manager{}))
	for _, tc := range []struct {
		name   string
		policy DelegatedHTTPTransport
		err    error
	}{{"nil-base", DelegatedHTTPTransport{}, nil}, {"negative-timeout", DelegatedHTTPTransport{Base: http.DefaultTransport, Timeout: -time.Second}, nil}, {"host-rejection", DelegatedHTTPTransport{}, errors.New("host blocked")}} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{}
			require.NoError(t, WithDelegatedHTTPTransport(func(context.Context, string, string) (DelegatedHTTPTransport, error) { return tc.policy, tc.err })(m))
			require.Error(t, m.configureDelegatedHTTPTransport(context.Background(), "fixture", delegatedTransportOptions()))
		})
	}
}
func TestDelegatedHTTPTransportDoesNotChangeUnconfiguredManager(t *testing.T) {
	require.NoError(t, (&Manager{}).configureDelegatedHTTPTransport(context.Background(), "fixture", nil))
}
