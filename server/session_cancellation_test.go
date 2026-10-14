package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
)

type sessionBlockingHandler struct {
	*serverproto.DefaultHandler
	entered chan context.Context
	release chan struct{}
}

func (*sessionBlockingHandler) Implements(method string) bool {
	return method == schema.MethodToolsCall
}
func (h *sessionBlockingHandler) CallTool(ctx context.Context, _ *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	h.entered <- ctx
	<-h.release
	return &schema.CallToolResult{}, nil
}

func TestSameRequestIDIsIsolatedAcrossSessions(t *testing.T) {
	for _, operation := range []string{"completion", "cancellation"} {
		t.Run(operation, func(t *testing.T) {
			firstTool := &sessionBlockingHandler{entered: make(chan context.Context, 1), release: make(chan struct{})}
			secondTool := &sessionBlockingHandler{entered: make(chan context.Context, 1), release: make(chan struct{})}
			first := newRequestIDAdapter(firstTool)
			second := NewAdapter(&Handler{Server: first.handler.Server, handler: secondTool, clientFeatures: make(map[string]bool)})
			firstDone, secondDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(firstDone)
				_, _ = first.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "first"}, client.WithJsonRpcRequestId(42))
			}()
			firstContext := <-firstTool.entered
			go func() {
				defer close(secondDone)
				_, _ = second.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "second"}, client.WithJsonRpcRequestId(42))
			}()
			secondContext := <-secondTool.entered
			if operation == "completion" {
				close(firstTool.release)
				<-firstDone
			} else {
				raw, _ := json.Marshal(map[string]any{"requestId": 42})
				if e := first.handler.Cancel(context.Background(), &jsonrpc.Notification{Params: raw}); e != nil {
					t.Errorf("cancel: %v", e)
				}
				if firstContext.Err() != context.Canceled {
					t.Error("own session did not receive cancellation")
				}
				close(firstTool.release)
				<-firstDone
			}
			if secondContext.Err() != nil {
				t.Errorf("%s in one session canceled another session: %v", operation, secondContext.Err())
			}
			close(secondTool.release)
			<-secondDone
			if first.handler.Server.activeContexts.Size() != 0 {
				t.Error("completed server operation leaked")
			}
			if len(first.handler.operations) != 0 || len(second.handler.operations) != 0 {
				t.Error("completed session operation leaked")
			}
		})
	}
}
