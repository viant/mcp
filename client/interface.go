package client

import (
	"context"
	"github.com/viant/mcp-protocol/schema"
)

// Interface defines the client interface for all exported methods
type Interface interface {
	// Initialize initializes the client
	Initialize(ctx context.Context) (*schema.InitializeResult, error)

	// ListResourceTemplates lists resource templates
	ListResourceTemplates(ctx context.Context, cursor *string) (*schema.ListResourceTemplatesResult, error)

	// ListResources lists resources
	ListResources(ctx context.Context, cursor *string) (*schema.ListResourcesResult, error)

	// ListPrompts lists prompts
	ListPrompts(ctx context.Context, cursor *string) (*schema.ListPromptsResult, error)

	// ListTools lists tools
	ListTools(ctx context.Context, cursor *string) (*schema.ListToolsResult, error)

	// ReadResource reads a resource
	ReadResource(ctx context.Context, params *schema.ReadResourceRequestParams) (*schema.ReadResourceResult, error)

	// GetPrompt gets a prompt
	GetPrompt(ctx context.Context, params *schema.GetPromptRequestParams) (*schema.GetPromptResult, error)

	// CallTool calls a tool
	CallTool(ctx context.Context, params *schema.CallToolRequestParams) (*schema.CallToolResult, error)

	// Complete completes a request
	Complete(ctx context.Context, params *schema.CompleteRequestParams) (*schema.CompleteResult, error)

	// Ping pings the server
	Ping(ctx context.Context, params *schema.PingRequestParams) (*schema.PingResult, error)

	// Subscribe subscribes to a resource
	Subscribe(ctx context.Context, params *schema.SubscribeRequestParams) (*schema.SubscribeResult, error)

	// Unsubscribe unsubscribes from a resource
	Unsubscribe(ctx context.Context, params *schema.UnsubscribeRequestParams) (*schema.UnsubscribeResult, error)

	// SetLevel sets the logging level
	SetLevel(ctx context.Context, params *schema.SetLevelRequestParams) (*schema.SetLevelResult, error)
}

// Ensure Client implements Interface
var _ Interface = (*Client)(nil)
