// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"fmt"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type managedClient struct {
	mgr        *Manager
	key        string
	serverName string
	entry      *entry
}

var _ mcpclient.Interface = (*managedClient)(nil)
var _ mcpclient.DiscoveryInterface = (*managedClient)(nil)
var _ mcpclient.SubscriptionInterface = (*managedClient)(nil)

func (c *managedClient) use() (mcpclient.Interface, func(), error) {
	client, err := c.mgr.beginUse(c.entry)
	if err != nil {
		return nil, func() {}, err
	}
	return client, func() { c.mgr.endUse(c.entry) }, nil
}

func (c *managedClient) Initialize(ctx context.Context, options ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	configured, err := c.mgr.Options(ctx, c.serverName)
	if err != nil {
		return nil, err
	}
	if usesUserTransportAuth(configured) {
		ctx = c.mgr.WithAuthTokenContext(ctx, c.serverName)
	}
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Initialize(ctx, options...)
}

// NegotiatedServer reuses the active client's completed handshake without a
// network RPC. Clients lacking this optional capability retain Initialize.
func (c *managedClient) NegotiatedServer() (*mcpschema.InitializeResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	negotiated, ok := client.(interface {
		NegotiatedServer() (*mcpschema.InitializeResult, error)
	})
	if !ok {
		return nil, fmt.Errorf("managed MCP client has no negotiated handshake")
	}
	return negotiated.NegotiatedServer()
}

func (c *managedClient) Discover(ctx context.Context, options ...mcpclient.RequestOption) (*mcpschema.DiscoverResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	discovery, ok := client.(mcpclient.DiscoveryInterface)
	if !ok {
		return nil, fmt.Errorf("managed MCP client does not support %s", mcpschema.MethodServerDiscover)
	}
	return discovery.Discover(ctx, options...)
}

func (c *managedClient) Listen(ctx context.Context, filter mcpschema.SubscriptionFilter, options ...mcpclient.RequestOption) (*mcpschema.SubscriptionsListenResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	listener, ok := client.(mcpclient.SubscriptionInterface)
	if !ok {
		return nil, fmt.Errorf("managed MCP client does not support %s", mcpschema.MethodSubscriptionsListen)
	}
	return listener.Listen(ctx, filter, options...)
}

func (c *managedClient) ListResourceTemplates(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.ListResourceTemplates(ctx, cursor, options...)
}

func (c *managedClient) ListResources(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.ListResources(ctx, cursor, options...)
}

func (c *managedClient) ListPrompts(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.ListPrompts(ctx, cursor, options...)
}

func (c *managedClient) ListTools(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return c.listTools(ctx, client, cursor, options...)
}

func (c *managedClient) ReadResource(ctx context.Context, params *mcpschema.ReadResourceRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.ReadResource(ctx, params, options...)
}

func (c *managedClient) GetPrompt(ctx context.Context, params *mcpschema.GetPromptRequestParams, options ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.GetPrompt(ctx, params, options...)
}

func (c *managedClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.CallTool(ctx, params, options...)
}

func (c *managedClient) Complete(ctx context.Context, params *mcpschema.CompleteRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Complete(ctx, params, options...)
}

func (c *managedClient) Ping(ctx context.Context, params *mcpschema.PingRequestParams, options ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Ping(ctx, params, options...)
}

func (c *managedClient) Subscribe(ctx context.Context, params *mcpschema.SubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Subscribe(ctx, params, options...)
}

func (c *managedClient) Unsubscribe(ctx context.Context, params *mcpschema.UnsubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Unsubscribe(ctx, params, options...)
}

func (c *managedClient) SetLevel(ctx context.Context, params *mcpschema.SetLevelRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.SetLevel(ctx, params, options...)
}

func (c *managedClient) ListRoots(ctx context.Context, params *mcpschema.ListRootsRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.ListRoots(ctx, params, options...)
}

func (c *managedClient) CreateMessage(ctx context.Context, params *mcpschema.CreateMessageRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.CreateMessage(ctx, params, options...)
}

func (c *managedClient) Elicit(ctx context.Context, params *mcpschema.ElicitRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	return client.Elicit(ctx, params, options...)
}

// ListResourcesWithParams forwards per-request catalog filters without changing
// the shared client's defaults or authentication scope.
func (c *managedClient) ListResourcesWithParams(ctx context.Context, params *mcpschema.ListResourcesRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	filtered, ok := client.(interface {
		ListResourcesWithParams(context.Context, *mcpschema.ListResourcesRequestParams, ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error)
	})
	if !ok {
		return nil, fmt.Errorf("managed MCP client does not support resource list parameters")
	}
	return filtered.ListResourcesWithParams(ctx, params, options...)
}

// ResourceListParamsSupported checks the actual pooled transport. An older
// client remains usable through its legacy catalog instead of falsely claiming
// it can forward per-request filter metadata.
func (c *managedClient) ResourceListParamsSupported(ctx context.Context) bool {
	client, release, err := c.use()
	if err != nil {
		return false
	}
	defer release()
	_, ok := client.(interface {
		ListResourcesWithParams(context.Context, *mcpschema.ListResourcesRequestParams, ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error)
	})
	return ok
}
