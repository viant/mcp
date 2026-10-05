package client

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	authcfg "github.com/viant/mcp/client/auth/config"
)

func TestRequestNoRetryNeverReplaysMissingSession(t *testing.T) {
	calls, reconnects := 0, 0
	tr := &mockTransport{send: func(ctx context.Context, _ *jsonrpc.Request) (*jsonrpc.Response, error) {
		calls++
		require.True(t, authcfg.NoRetry(ctx))
		return nil, errors.New("session 'old' not found")
	}}
	c := New("test", "1", tr, WithReconnect(func(context.Context) (transport.Transport, error) { reconnects++; return tr, nil }))
	c.initialized = true
	_, err := c.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "effect"}, WithNoRetry())
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.Zero(t, reconnects)
}
