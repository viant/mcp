package client

import (
	"context"

	"github.com/viant/mcp-protocol/schema"
)

// Interface defines the clientHandler interface for all exported methods
type Interface interface {
	// Initialize initializes the clientHandler
	Initialize(ctx context.Context, options ...RequestOption) (*schema.InitializeResult, error)

	// ListResourceTemplates lists resource templates
	ListResourceTemplates(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListResourceTemplatesResult, error)

	// ListResources lists resources
	ListResources(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListResourcesResult, error)

	// ListPrompts lists prompts
	ListPrompts(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListPromptsResult, error)

	// ListTools lists tools
	ListTools(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListToolsResult, error)

	// ReadResource reads a resource
	ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams, options ...RequestOption) (*schema.ReadResourceResult, error)

	// GetPrompt gets a prompt
	GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams, options ...RequestOption) (*schema.GetPromptResult, error)

	// CallTool calls a tool
	CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...RequestOption) (*schema.CallToolResult, error)

	// Complete completes a request
	Complete(ctx context.Context, params *schema.CompleteRequestParams, options ...RequestOption) (*schema.CompleteResult, error)

	// Ping pings the server
	Ping(ctx context.Context, params *schema.PingRequestParams, options ...RequestOption) (*schema.PingResult, error)

	// Subscribe subscribes to a resource
	Subscribe(ctx context.Context, params *schema.SubscribeRequestParams, options ...RequestOption) (*schema.SubscribeResult, error)

	// Unsubscribe unsubscribes from a resource
	Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams, options ...RequestOption) (*schema.UnsubscribeResult, error)

	// SetLevel sets the logging level
	SetLevel(ctx context.Context, params *schema.SetLevelRequestParams, options ...RequestOption) (*schema.SetLevelResult, error)

	// ----- New operations defined in mcp-protocol/clientHandler/operations.go -----

	// ListRoots lists clientHandler roots (clientHandler side capability discovery)
	ListRoots(ctx context.Context, params *schema.ListRootsRequestParams, options ...RequestOption) (*schema.ListRootsResult, error)

	// CreateMessage creates a sampling message on the clientHandler side
	CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams, options ...RequestOption) (*schema.CreateMessageResult, error)

	// Elicit is a server-initiated request asking the clientHandler to elicit additional information from the end-user
	Elicit(ctx context.Context, params *schema.ElicitRequestParams, options ...RequestOption) (*schema.ElicitResult, error)
}

// Ensure Client implements Interface
var _ Interface = (*Client)(nil)
