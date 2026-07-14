package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
)

func TestToolProtocolErrors(t *testing.T) {
	testCases := []struct {
		name         string
		options      []Option
		wantProtocol bool
	}{
		{name: "default converts error"},
		{name: "opt-in preserves error", options: []Option{WithToolProtocolErrors()}, wantProtocol: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newMissingToolHandler(t, testCase.options...)
			request, err := jsonrpc.NewRequest(schema.MethodToolsCall, &schema.CallToolRequestParams{Name: "missing"})
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			response := &jsonrpc.Response{}
			handler.Serve(context.Background(), request, response)

			if testCase.wantProtocol {
				if response.Error == nil || response.Error.Code != jsonrpc.MethodNotFound {
					t.Fatalf("Serve() error = %#v, want method-not-found JSON-RPC error", response.Error)
				}
				return
			}
			if response.Error != nil {
				t.Fatalf("Serve() error = %#v, want converted tool result", response.Error)
			}
			var result schema.CallToolResult
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatalf("unmarshal CallToolResult: %v", err)
			}
			if result.IsError == nil || !*result.IsError {
				t.Fatalf("Serve() result = %#v, want isError=true", result)
			}
		})
	}
}

func TestToolProtocolErrorsDoesNotAffectAuthorization(t *testing.T) {
	handler := newMissingToolHandler(t,
		WithToolProtocolErrors(),
		WithJRPCAuthorizer(func(context.Context, *jsonrpc.Request, *jsonrpc.Response) (*authorization.Token, error) {
			return nil, errors.New("authorization unavailable")
		}),
	)
	request, err := jsonrpc.NewRequest(schema.MethodToolsCall, &schema.CallToolRequestParams{Name: "missing"})
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	response := &jsonrpc.Response{}
	handler.Serve(context.Background(), request, response)
	if response.Error == nil || response.Error.Code != jsonrpc.InternalError {
		t.Fatalf("Serve() error = %#v, want authorization internal error", response.Error)
	}
}

func newMissingToolHandler(t *testing.T, options ...Option) *Handler {
	t.Helper()
	newHandler := serverproto.WithDefaultHandler(context.Background(), func(handler *serverproto.DefaultHandler) error {
		return serverproto.RegisterTool[struct{}, struct{}](handler.Registry, "registered", "registered tool", func(context.Context, struct{}) (*schema.CallToolResult, *jsonrpc.Error) {
			return &schema.CallToolResult{}, nil
		})
	})
	options = append([]Option{WithNewHandler(newHandler)}, options...)
	instance, err := New(options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler, ok := instance.NewHandler(context.Background(), nil).(*Handler)
	if !ok {
		t.Fatalf("NewHandler() returned unexpected type")
	}
	return handler
}
