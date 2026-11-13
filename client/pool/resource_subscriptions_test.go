package manager

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protoserver "github.com/viant/mcp-protocol/server"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
	mcpserver "github.com/viant/mcp/server"
)

type catalogSubscription struct {
	filter   schema.SubscriptionFilter
	notifier transport.Notifier
	done     <-chan struct{}
}
type subscriptionPeer struct {
	capabilities     schema.ServerCapabilities
	accept           func(schema.SubscriptionFilter) schema.SubscriptionFilter
	subscriptions    chan catalogSubscription
	notifiers        chan transport.Notifier
	calls            atomic.Int32
	ttlMs            int
	blockAt          int32
	entered, release chan struct{}
}
type subscriptionHandler struct {
	*protoserver.DefaultHandler
	peer *subscriptionPeer
}

func (h *subscriptionHandler) FilterSubscriptions(ctx context.Context, filter schema.SubscriptionFilter) schema.SubscriptionFilter {
	h.peer.subscriptions <- catalogSubscription{filter, h.Notifier, ctx.Done()}
	if h.peer.accept != nil {
		return h.peer.accept(filter)
	}
	return filter
}
func (h *subscriptionHandler) ListTools(ctx context.Context, _ *jsonrpc.TypedRequest[*schema.ListToolsRequest]) (*schema.ListToolsResult, *jsonrpc.Error) {
	call := h.peer.calls.Add(1)
	if call == h.peer.blockAt {
		close(h.peer.entered)
		select {
		case <-h.peer.release:
		case <-ctx.Done():
			return nil, jsonrpc.NewInternalError(ctx.Err().Error(), nil).WithCause(ctx.Err())
		}
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: fmt.Sprintf("tool-%d", call), InputSchema: schema.ToolInputSchema{Type: "object"}}}, ResultType: schema.ResultTypeComplete, CacheScope: schema.ListToolsResultCacheScopePrivate, TtlMs: h.peer.ttlMs}, nil
}
func (h *subscriptionHandler) Implements(method string) bool {
	return method == schema.MethodToolsList || h.DefaultHandler.Implements(method)
}

func subscriptionFixture(t *testing.T, peer *subscriptionPeer, version string) (*Manager, *managedClient, context.Context, func()) {
	t.Helper()
	peer.subscriptions = make(chan catalogSubscription, 4)
	peer.notifiers = make(chan transport.Notifier, 16)
	server, err := mcpserver.New(mcpserver.WithNewHandler(func(_ context.Context, notifier transport.Notifier, log logger.Logger, client protoclient.Operations) (protoserver.Handler, error) {
		peer.notifiers <- notifier
		base := protoserver.NewDefaultHandler(notifier, log, client)
		base.ServerCapabilities = &peer.capabilities
		return &subscriptionHandler{base, peer}, nil
	}))
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.HTTP(context.Background(), "").Handler)
	t.Cleanup(httpServer.Close)
	settings := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: version, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: httpServer.URL + "/mcp"}}}}
	mgr, err := New(&catalogProvider{cfg: settings}, WithPersistentToolCatalog())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(authctx.WithCanonicalUserID(context.Background(), "alice"), 10*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { mgr.CloseConversation("chat") })
	client, err := mgr.Get(ctx, "chat", "server")
	require.NoError(t, err)
	return mgr, client.(*managedClient), ctx, httpServer.CloseClientConnections
}
func receiveSubscription(t *testing.T, peer *subscriptionPeer) catalogSubscription {
	t.Helper()
	select {
	case subscription := <-peer.subscriptions:
		return subscription
	case <-time.After(time.Second):
		t.Fatal("native subscription did not open")
		return catalogSubscription{}
	}
}
func sendToolChange(t *testing.T, subscription catalogSubscription, mgr *Manager) {
	t.Helper()
	generation := mgr.ToolCatalogGeneration("server")
	require.NoError(t, subscription.notifier.Notify(context.Background(), &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/tools/list_changed"}))
	require.Eventually(t, func() bool { return mgr.ToolCatalogGeneration("server") > generation }, time.Second, time.Millisecond)
}
func TestPoolCatalogSubscriptionUsesNegotiatedCapabilities(t *testing.T) {
	enabled, disabled := true, false
	for _, test := range []struct {
		name                     string
		version                  string
		tools, resources         *bool
		accept                   func(schema.SubscriptionFilter) schema.SubscriptionFilter
		subscribed, toolAccepted bool
	}{
		{name: "tools only", tools: &enabled, subscribed: true, toolAccepted: true},
		{name: "tools and resources", tools: &enabled, resources: &enabled, subscribed: true, toolAccepted: true},
		{name: "resources only", resources: &enabled, subscribed: true},
		{name: "missing list-change capabilities"},
		{name: "disabled list-change capabilities", tools: &disabled, resources: &disabled},
		{name: "legacy delivery unchanged", version: schema.LegacyProtocolVersion, tools: &enabled, resources: &enabled},
		{name: "partial acknowledgement retains tools", tools: &enabled, resources: &enabled, subscribed: true, toolAccepted: true, accept: func(f schema.SubscriptionFilter) schema.SubscriptionFilter { f.ResourcesListChanged = nil; return f }},
		{name: "unrequested acknowledgement", tools: &enabled, subscribed: true, accept: func(schema.SubscriptionFilter) schema.SubscriptionFilter {
			return schema.SubscriptionFilter{ResourcesListChanged: &enabled}
		}},
		{name: "empty acknowledgement", tools: &enabled, subscribed: true, accept: func(schema.SubscriptionFilter) schema.SubscriptionFilter { return schema.SubscriptionFilter{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			peer := &subscriptionPeer{capabilities: schema.ServerCapabilities{}, accept: test.accept}
			if test.tools != nil {
				peer.capabilities.Tools = &schema.ServerCapabilitiesTools{ListChanged: test.tools}
			}
			if test.resources != nil {
				peer.capabilities.Resources = &schema.ServerCapabilitiesResources{ListChanged: test.resources}
			}
			version := test.version
			if version == "" {
				version = schema.LatestProtocolVersion
			}
			mgr, client, ctx, _ := subscriptionFixture(t, peer, version)
			require.Zero(t, peer.calls.Load(), "subscription must not eagerly load catalogs")
			negotiated, err := client.NegotiatedServer()
			require.NoError(t, err)
			require.Equal(t, peer.capabilities.Tools, negotiated.Capabilities.Tools)
			if !test.subscribed {
				require.Empty(t, peer.subscriptions)
				require.Nil(t, client.entry.client.(*sensitiveClient).subscriptionCancel)
				if version == schema.LegacyProtocolVersion {
					_, err := client.ListTools(ctx, nil)
					require.NoError(t, err)
					sendToolChange(t, catalogSubscription{notifier: <-peer.notifiers}, mgr)
					current, err := client.ListTools(ctx, nil)
					require.NoError(t, err)
					require.Equal(t, "tool-2", current.Tools[0].Name, "legacy session notifications retain catalog invalidation")
				}
				return
			}
			subscription := receiveSubscription(t, peer)
			requested := func(value *bool) bool { return value != nil && *value }
			require.Equal(t, requested(test.tools), requested(subscription.filter.ToolsListChanged))
			require.Equal(t, requested(test.resources), requested(subscription.filter.ResourcesListChanged))
			require.Nil(t, subscription.filter.PromptsListChanged)
			if test.name == "empty acknowledgement" || test.name == "unrequested acknowledgement" {
				select {
				case <-subscription.done:
				case <-ctx.Done():
					t.Fatal("unaccepted stream was not cancelled")
				}
				require.Zero(t, mgr.ToolCatalogGeneration("server"), "declining a filter must not retire the connection")
			}
			if !test.toolAccepted {
				return
			}
			for range 2 {
				_, err := client.ListTools(ctx, nil)
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, peer.calls.Load())
			sendToolChange(t, subscription, mgr)
			require.EqualValues(t, 1, peer.calls.Load(), "notifications invalidate without eager I/O")
			current, err := client.ListTools(ctx, nil)
			require.NoError(t, err)
			require.Equal(t, "tool-2", current.Tools[0].Name)
			require.EqualValues(t, 2, peer.calls.Load())
		})
	}
}
func TestPoolNativeToolSubscriptionFencesInflightCatalog(t *testing.T) {
	enabled := true
	peer := &subscriptionPeer{capabilities: schema.ServerCapabilities{Tools: &schema.ServerCapabilitiesTools{ListChanged: &enabled}}, blockAt: 2, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(peer.release) }) }
	t.Cleanup(release)
	mgr, client, ctx, _ := subscriptionFixture(t, peer, schema.LatestProtocolVersion)
	subscription := receiveSubscription(t, peer)
	_, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	sendToolChange(t, subscription, mgr)
	pending := startReceiptRead(ctx, client)
	select {
	case <-peer.entered:
	case <-ctx.Done():
		t.Fatal("inflight tools/list did not start")
	}
	sendToolChange(t, subscription, mgr)
	current, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "tool-3", current.Tools[0].Name)
	release()
	require.NoError(t, receiptResult(t, pending).err)
	current, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "tool-3", current.Tools[0].Name, "late old-generation result cannot replace the notified catalog")
	require.EqualValues(t, 3, peer.calls.Load())
}
func TestPoolToolSubscriptionDisconnectRetiresConnection(t *testing.T) {
	enabled := true
	peer := &subscriptionPeer{capabilities: schema.ServerCapabilities{Tools: &schema.ServerCapabilitiesTools{ListChanged: &enabled}}}
	mgr, client, ctx, disconnect := subscriptionFixture(t, peer, schema.LatestProtocolVersion)
	receiveSubscription(t, peer)
	_, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	generation := mgr.ToolCatalogGeneration("server")
	disconnect()
	require.Eventually(t, func() bool {
		return !mgr.IsCurrentClient(ctx, "chat", "server", client) && mgr.ToolCatalogGeneration("server") > generation
	}, time.Second, time.Millisecond)
	require.Equal(t, [2]uint64{}, mgr.ResourceCatalogGeneration(ctx, "server"), "a tools-only stream does not invalidate resource catalogs")
}

func TestPoolResourceSubscriptionDisconnectRefreshesToolCatalog(t *testing.T) {
	enabled := true
	peer := &subscriptionPeer{capabilities: schema.ServerCapabilities{Tools: &schema.ServerCapabilitiesTools{}, Resources: &schema.ServerCapabilitiesResources{ListChanged: &enabled}}, ttlMs: 60000}
	mgr, client, ctx, disconnect := subscriptionFixture(t, peer, schema.LatestProtocolVersion)
	subscription := receiveSubscription(t, peer)
	require.Nil(t, subscription.filter.ToolsListChanged)
	first, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "tool-1", first.Tools[0].Name)
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, peer.calls.Load())
	generation := mgr.ToolCatalogGeneration("server")
	resourceGeneration := mgr.ResourceCatalogGeneration(ctx, "server")
	require.NoError(t, subscription.notifier.Notify(ctx, &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/resources/list_changed"}))
	require.Eventually(t, func() bool { return mgr.ResourceCatalogGeneration(ctx, "server") != resourceGeneration }, time.Second, time.Millisecond)
	require.Equal(t, generation, mgr.ToolCatalogGeneration("server"), "ordinary resource changes preserve the tool catalog")
	_, err = client.ListTools(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, peer.calls.Load())
	disconnect()
	require.Eventually(t, func() bool {
		return !mgr.IsCurrentClient(ctx, "chat", "server", client) && mgr.ToolCatalogGeneration("server") > generation
	}, time.Second, time.Millisecond)
	// Let retirement finish closing the old HTTP transport before reconnecting.
	client.entry.client.(*sensitiveClient).Close()
	replacement, err := mgr.Get(ctx, "chat", "server")
	require.NoError(t, err)
	require.NotSame(t, client, replacement)
	receiveSubscription(t, peer)
	current, err := replacement.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "tool-2", current.Tools[0].Name, "connection retirement fences immutable tool metadata from the old process")
	require.EqualValues(t, 2, peer.calls.Load())
}
