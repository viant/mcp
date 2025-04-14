package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/schema"
)

type Client struct{ transport.Transport }

func (c *Client) ListRoots(ctx context.Context, params *schema.ListRootsRequestParams) (*schema.ListRootsResult, *jsonrpc.Error) {
	return send[schema.ListRootsRequestParams, schema.ListRootsResult](ctx, c, schema.MethodRootsList, params)
}

// CreateMessage creates sampling message
func (c *Client) CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return send[schema.CreateMessageRequestParams, schema.CreateMessageResult](ctx, c, schema.MethodRootsList, params)
}

func send[P any, R any](ctx context.Context, client *Client, method string, parameters *P) (*R, *jsonrpc.Error) {
	clientTransport := client.Transport
	req, err := jsonrpc.NewRequest(method, parameters)
	if err != nil {
		return nil, jsonrpc.NewInvalidRequest(err.Error(), nil)
	}
	response, err := clientTransport.Send(ctx, req)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), req.Params)
	}
	var result R
	err = json.Unmarshal(response.Result, &result)
	return &result, response.Error
}
