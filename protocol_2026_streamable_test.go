package mcp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type julyProtocolClientHandler struct {
	notifications chan string
}

func (*julyProtocolClientHandler) Notify(context.Context, *jsonrpc.Notification) error { return nil }
func (*julyProtocolClientHandler) NextRequestID() jsonrpc.RequestId                    { return 1 }
func (*julyProtocolClientHandler) LastRequestID() jsonrpc.RequestId                    { return 1 }
func (*julyProtocolClientHandler) Implements(string) bool                              { return false }
func (*julyProtocolClientHandler) Init(context.Context, *schema.ClientCapabilities)    {}
func (h *julyProtocolClientHandler) OnNotification(_ context.Context, notification *jsonrpc.Notification) {
	if h.notifications == nil {
		return
	}
	select {
	case h.notifications <- notification.Method:
	default:
	}
}
func (*julyProtocolClientHandler) ListRoots(context.Context, *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	return &schema.ListRootsResult{}, nil
}
func (*julyProtocolClientHandler) CreateMessage(context.Context, *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return &schema.CreateMessageResult{}, nil
}
func (*julyProtocolClientHandler) Elicit(context.Context, *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	return &schema.ElicitResult{Action: schema.ElicitResultActionDecline}, nil
}

var _ pclient.Handler = (*julyProtocolClientHandler)(nil)

func TestJulyStreamableHTTPIsStatelessEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	newHandler := serverproto.WithDefaultHandler(ctx, func(*serverproto.DefaultHandler) error { return nil })
	server, err := mcpserver.New(
		mcpserver.WithNewHandler(newHandler),
		mcpserver.WithImplementation(schema.Implementation{Name: "july-server", Version: "1.0"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := server.HTTP(ctx, listener.Addr().String())
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Close()

	options := &ClientOptions{
		Name:            "july-client",
		Version:         "1.0",
		ProtocolVersion: schema.LatestProtocolVersion,
		Transport: ClientTransport{
			Type: "streamable",
			ClientTransportHTTP: ClientTransportHTTP{
				URL: "http://" + listener.Addr().String() + "/mcp",
			},
		},
	}
	clientHandler := &julyProtocolClientHandler{notifications: make(chan string, 1)}
	client, err := NewClientWithContext(ctx, clientHandler, options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ping, err := client.Ping(ctx, &schema.PingRequestParams{})
	if err != nil {
		t.Fatal(err)
	}
	if ping == nil {
		t.Fatal("expected ping result")
	}

	listenCtx, stopListening := context.WithCancel(ctx)
	listenError := make(chan error, 1)
	go func() {
		_, err := client.Listen(listenCtx, schema.SubscriptionFilter{})
		listenError <- err
	}()
	select {
	case method := <-clientHandler.notifications:
		if method != schema.MethodNotificationSubscriptionsAcknowledged {
			t.Fatalf("unexpected subscription notification %q", method)
		}
	case <-ctx.Done():
		t.Fatal("subscription acknowledgement was not received")
	}
	stopListening()
	select {
	case err := <-listenError:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("expected cancelled subscription, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("subscription request did not stop after cancellation")
	}
	if _, err := client.Ping(ctx, &schema.PingRequestParams{}); err != nil {
		t.Fatalf("client was poisoned by subscription cancellation: %v", err)
	}
}
