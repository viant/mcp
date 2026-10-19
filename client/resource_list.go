package client

import (
	"context"

	"github.com/viant/mcp-protocol/schema"
)

// ResourceListInterface is an optional extension for callers that need to pass
// request metadata when listing resources. Interface remains backward compatible.
type ResourceListInterface interface {
	ListResourcesWithParams(ctx context.Context, params *schema.ListResourcesRequestParams, options ...RequestOption) (*schema.ListResourcesResult, error)
}

var _ ResourceListInterface = (*Client)(nil)

// ListResourcesWithParams lists resources with caller-supplied pagination and
// metadata. Metadata extensions are opaque to the client. Like ListResources,
// this method uses the client's standard authentication and protocol handling.
// It does not modify params.
func (c *Client) ListResourcesWithParams(ctx context.Context, params *schema.ListResourcesRequestParams, options ...RequestOption) (*schema.ListResourcesResult, error) {
	if params == nil {
		params = &schema.ListResourcesRequestParams{}
	}
	return send[schema.ListResourcesRequestParams, schema.ListResourcesResult](ctx, c, schema.MethodResourcesList, params, options...)
}
