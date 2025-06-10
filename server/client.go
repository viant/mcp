package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

// Client implements mcp-protocol/client.Operations for the handler side. It allows
// handler implementers to invoke client-side RPC methods over the same transport
// channel on which the original request arrived.
type Client struct {
	implements map[string]bool
	transport.Transport
	transport.Sequencer
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

func (c *Client) NextRequestId() uint64 {
	if c.Sequencer != nil {
		id := c.NextRequestID()
		ret, _ := jsonrpc.AsRequestIntId(id)
		return uint64(ret)
	}
	return 0
}

func (c *Client) ListRoots(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	if request.Id == 0 {
		request.Id = c.NextRequestId()
	}
	request.Method = schema.MethodRootsList
	return send[schema.ListRootsRequestParams, schema.ListRootsResult](ctx, c, schema.MethodRootsList, request.Id, request.Request.Params)
}

// CreateMessage creates a sampling message on the client side.
func (c *Client) CreateMessage(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	if request.Id == 0 {
		request.Id = c.NextRequestId()
	}
	request.Method = schema.MethodSamplingCreateMessage
	return send[schema.CreateMessageRequestParams, schema.CreateMessageResult](ctx, c, schema.MethodSamplingCreateMessage, request.Id, &request.Request.Params)
}

// Experimental/Proposed method names that are not yet part of the stable schema

// Elicit asks the client to elicit additional information from the user.
func (c *Client) Elicit(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	if request.Id == 0 {
		request.Id = c.NextRequestId()
	}
	request.Method = schema.MethodElicitationCreate
	return send[schema.ElicitRequestParams, schema.ElicitResult](ctx, c, schema.MethodElicitationCreate, request.Id, &request.Request.Params)
}

// CreateUserInteraction requests that the client presents an interaction UI to
// the user and returns their response.
func (c *Client) CreateUserInteraction(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CreateUserInteractionRequest]) (*schema.CreateUserInteractionResult, *jsonrpc.Error) {
	if request.Id == 0 {
		request.Id = c.NextRequestId()
	}
	request.Method = schema.MethodElicitationCreate
	return send[schema.CreateUserInteractionRequestParams, schema.CreateUserInteractionResult](ctx, c, schema.MethodInteractionCreate, request.Id, &request.Request.Params)
}

// send marshals parameters, sends the request and unmarshals the result.
func send[P any, R any](ctx context.Context, client *Client, method string, id uint64, parameters *P) (*R, *jsonrpc.Error) {
	clientTransport := client.Transport
	req, err := jsonrpc.NewRequest(method, parameters)
	req.Id = id
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
func NewClient(implements map[string]bool, aTransport transport.Transport) *Client {
	if implements == nil {
		implements = make(map[string]bool)
	}
	seq, _ := aTransport.(transport.Sequencer)
	return &Client{implements: implements, Transport: aTransport, Sequencer: seq}
}

var _ client.Operations = &Client{}
