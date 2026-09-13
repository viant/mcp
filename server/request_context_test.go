package server

import (
	"context"
	"errors"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
	"testing"
)

type requestScopedHandler struct {
	protocolserver.Handler
	observed func(context.Context) bool
}

func (h *requestScopedHandler) Implements(string) bool { return false }
func (h *requestScopedHandler) ImplementsContext(ctx context.Context, _ string) bool {
	return h.observed(ctx)
}
func (h *requestScopedHandler) CallTool(ctx context.Context, _ *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	if !h.observed(ctx) {
		return nil, jsonrpc.NewInternalError("request snapshot missing", nil)
	}
	return &schema.CallToolResult{}, nil
}

func TestRequestContextPrecedesAvailabilityAndDispatch(t *testing.T) {
	type key struct{}
	h := newMissingToolHandler(t, WithRequestContext(func(ctx context.Context) (context.Context, error) { return context.WithValue(ctx, key{}, true), nil }))
	observed := 0
	h.handler = &requestScopedHandler{Handler: h.handler, observed: func(ctx context.Context) bool {
		value, _ := ctx.Value(key{}).(bool)
		if value {
			observed++
		}
		return value
	}}
	request, err := jsonrpc.NewRequest(schema.MethodToolsCall, &schema.CallToolRequestParams{Name: "dynamic"})
	if err != nil {
		t.Fatal(err)
	}
	response := &jsonrpc.Response{}
	h.Serve(context.Background(), request, response)
	if response.Error != nil || observed != 2 {
		t.Fatalf("error=%v observations=%d", response.Error, observed)
	}
}

func TestRequestContextPreparation(t *testing.T) {
	type key struct{}
	for _, test := range []struct {
		name       string
		fail       bool
		nilContext bool
	}{{name: "prepared"}, {name: "error", fail: true}, {name: "nil", nilContext: true}} {
		t.Run(test.name, func(t *testing.T) {
			authorized := false
			h := newMissingToolHandler(t, WithToolProtocolErrors(), WithRequestContext(func(ctx context.Context) (context.Context, error) {
				if test.fail {
					return nil, errors.New("private secret")
				}
				if test.nilContext {
					return nil, nil
				}
				return context.WithValue(ctx, key{}, true), nil
			}), WithJRPCAuthorizer(func(ctx context.Context, _ *jsonrpc.Request, _ *jsonrpc.Response) (*authorization.Token, error) {
				authorized, _ = ctx.Value(key{}).(bool)
				return nil, nil
			}))
			request, err := jsonrpc.NewRequest(schema.MethodToolsCall, &schema.CallToolRequestParams{Name: "missing"})
			if err != nil {
				t.Fatal(err)
			}
			response := &jsonrpc.Response{}
			h.Serve(context.Background(), request, response)
			if test.fail || test.nilContext {
				if authorized || response.Error == nil || response.Error.Message != "request context preparation failed" {
					t.Fatalf("authorized=%v response=%+v", authorized, response)
				}
			} else if !authorized || response.Error == nil || response.Error.Code != jsonrpc.MethodNotFound {
				t.Fatalf("authorized=%v response=%+v", authorized, response)
			}
		})
	}
}
