package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	protoclient "github.com/viant/mcp-protocol/client"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
)

type localNotificationCapture struct {
	mcpclient.Interface
	handler protoclient.Handler
}

func (c *localNotificationCapture) SetNotificationHandler(h protoclient.Handler) { c.handler = h }
func TestLocalFactoryInstallsStandardNotificationReceiverWithTrustedCreationScope(t *testing.T) {
	mgr, err := New(nil)
	require.NoError(t, err)
	alice := authctx.WithCanonicalUserID(context.Background(), "alice")
	bob := authctx.WithCanonicalUserID(context.Background(), "bob")
	raw := &localNotificationCapture{}
	require.NoError(t, mgr.RegisterLocal(context.Background(), "native", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "native"}, func(context.Context) (mcpclient.Interface, error) { return raw, nil }))
	_, err = mgr.Get(alice, "conversation", "native")
	require.NoError(t, err)
	require.NotNil(t, raw.handler)
	// A missing/foreign callback context cannot change the authenticated receiver.
	raw.handler.OnNotification(bob, &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/resources/list_changed"})
	require.Equal(t, [2]uint64{0, 1}, mgr.ResourceCatalogGeneration(alice, "native"))
	require.Equal(t, [2]uint64{}, mgr.ResourceCatalogGeneration(bob, "native"))
	require.Zero(t, mgr.ToolCatalogGeneration("native"))
}

func TestCustomFactoryInstallsStandardNotificationReceiver(t *testing.T) {
	alice := authctx.WithCanonicalUserID(context.Background(), "alice")
	bob := authctx.WithCanonicalUserID(context.Background(), "bob")
	raw := &localNotificationCapture{}
	mgr, err := New(localInventoryProviderForNotifications{}, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return raw, nil }))
	require.NoError(t, err)
	_, err = mgr.Get(alice, "conversation", "native")
	require.NoError(t, err)
	require.NotNil(t, raw.handler)
	raw.handler.OnNotification(bob, &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/resources/list_changed"})
	require.Equal(t, [2]uint64{0, 1}, mgr.ResourceCatalogGeneration(alice, "native"))
	require.Equal(t, [2]uint64{}, mgr.ResourceCatalogGeneration(bob, "native"))
	require.Zero(t, mgr.ToolCatalogGeneration("native"))
}

type localInventoryProviderForNotifications struct{}

func (localInventoryProviderForNotifications) Options(context.Context, string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}}, nil
}
