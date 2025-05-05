package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/internal/conv"
)

type activeContext struct {
	context.Context
	context.CancelFunc
}

func newActiveContext(ctx context.Context, cancel context.CancelFunc, request *jsonrpc.Request) (*activeContext, context.Context) {
	if progressToken := extractProgressToken(request); progressToken != nil {
		ctx = context.WithValue(ctx, schema.TokenProgressContextKey, *progressToken)
	}
	return &activeContext{
		Context:    ctx,
		CancelFunc: cancel,
	}, ctx
}

func extractProgressToken(request *jsonrpc.Request) *schema.ProgressToken {
	var ret *schema.ProgressToken
	meta := parameterMeta(request)
	if value, ok := meta["progressToken"]; ok {
		progressToken := schema.ProgressToken(conv.AsInt(value))
		ret = &progressToken
	}
	return ret
}

func parameterMeta(request *jsonrpc.Request) map[string]interface{} {
	type paramsMeta struct {
		// to attach additional metadata to their responses.
		Meta map[string]interface{} `json:"_meta,omitempty" yaml:"_meta,omitempty" `
	}
	meta := &paramsMeta{}
	if err := json.Unmarshal(request.Params, meta); err == nil {
		return meta.Meta
	}
	return make(map[string]interface{})
}
