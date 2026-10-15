package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
)

type completionHandler struct {
	*serverproto.DefaultHandler
	fastEntered, slowEntered, finishFast, finishSlow chan struct{}
	slowCanceled                                     chan bool
}

func (h *completionHandler) Implements(method string) bool { return method == schema.MethodToolsCall }
func (h *completionHandler) CallTool(ctx context.Context, req *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	if req.Request.Params.Name == "fast" {
		close(h.fastEntered)
		<-h.finishFast
	} else {
		close(h.slowEntered)
		select {
		case <-ctx.Done():
			h.slowCanceled <- true
		case <-h.finishSlow:
			h.slowCanceled <- false
		}
	}
	return &schema.CallToolResult{}, nil
}
func TestCompletionDoesNotCancelReusedRequestID(t *testing.T) {
	h := &completionHandler{fastEntered: make(chan struct{}), slowEntered: make(chan struct{}), finishFast: make(chan struct{}), finishSlow: make(chan struct{}), slowCanceled: make(chan bool, 1)}
	adapter := newRequestIDAdapter(h)
	fastDone := make(chan struct{})
	slowDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		_, _ = adapter.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "fast"}, client.WithJsonRpcRequestId(42))
	}()
	<-h.fastEntered
	go func() {
		defer close(slowDone)
		_, _ = adapter.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "slow"}, client.WithJsonRpcRequestId(42))
	}()
	<-h.slowEntered
	close(h.finishFast)
	<-fastDone
	select {
	case <-slowDone:
		t.Fatal("completing another request canceled the active request with a reused ID")
	case <-time.After(20 * time.Millisecond):
	}
	// A cancellation notification must still reach the current registered request.
	raw, _ := json.Marshal(map[string]any{"requestId": 42})
	if err := adapter.handler.Cancel(context.Background(), &jsonrpc.Notification{Params: raw}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("notification failed to cancel registered request")
	}
	if !<-h.slowCanceled {
		t.Fatal("registered request did not observe cancellation")
	}
	if adapter.handler.activeContexts.Size() != 0 {
		t.Fatal("completed request registration leaked")
	}
}
