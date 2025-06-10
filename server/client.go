package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
)

// Client implements mcp-protocol/client.Operations for the handler side. It allows
// handler implementers to invoke client-side RPC methods over the same transport
// channel on which the original request arrived.
type Client struct {
	implements map[string]bool
	transport.Transport
}

func (c *Client) ListRoots(ctx context.Context, params *schema.ListRootsRequestParams) (*schema.ListRootsResult, *jsonrpc.Error) {
	return send[schema.ListRootsRequestParams, schema.ListRootsResult](ctx, c, schema.MethodRootsList, params)
}

// CreateMessage creates a sampling message on the client side.
func (c *Client) CreateMessage(ctx context.Context, params *schema.CreateMessageRequestParams) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return send[schema.CreateMessageRequestParams, schema.CreateMessageResult](ctx, c, schema.MethodSamplingCreateMessage, params)
}

func (c *Client) Init(ctx context.Context, capabilities *schema.ClientCapabilities) {
	if capabilities.Elicitation != nil {
		c.implements[schema.MethodElicitationCreate] = true
	}
	if capabilities.Roots != nil {
		c.implements[schema.MethodRootsList] = true
	}
	if capabilities.UserInteraction != nil {
		c.implements[schema.MethodInteractionCreate] = true
	}
	if capabilities.Sampling != nil {
		c.implements[schema.MethodSamplingCreateMessage] = true
	}

}

func (c *Client) Implements(method string) bool {
	return c.implements[method]
}

// Experimental/Proposed method names that are not yet part of the stable schema

// Elicit asks the client to elicit additional information from the user.
func (c *Client) Elicit(ctx context.Context, params *schema.ElicitRequestParams) (*schema.ElicitResult, *jsonrpc.Error) {
	return send[schema.ElicitRequestParams, schema.ElicitResult](ctx, c, schema.MethodElicitationCreate, params)
}

// CreateUserInteraction requests that the client presents an interaction UI to
// the user and returns their response.
func (c *Client) CreateUserInteraction(ctx context.Context, params *schema.CreateUserInteractionRequestParams) (*schema.CreateUserInteractionResult, *jsonrpc.Error) {
	return send[schema.CreateUserInteractionRequestParams, schema.CreateUserInteractionResult](ctx, c, schema.MethodInteractionCreate, params)
}

// send marshals parameters, sends the request and unmarshals the result.
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
	if unmarshalErr := json.Unmarshal(response.Result, &result); unmarshalErr != nil {
		return nil, jsonrpc.NewInternalError(unmarshalErr.Error(), nil)
	}

	return &result, response.Error
}

// NewClient create a client
func NewClient(implements map[string]bool, transport transport.Transport) *Client {
	if implements == nil {
		implements = make(map[string]bool)
	}
	return &Client{implements: implements, Transport: transport}
}
