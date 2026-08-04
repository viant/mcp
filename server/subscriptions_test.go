package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

type subscriptionNotifier struct {
	notifications chan *jsonrpc.Notification
}

func (n *subscriptionNotifier) Notify(_ context.Context, notification *jsonrpc.Notification) error {
	n.notifications <- notification
	return nil
}

func TestListenAcknowledgesBeforeClosing(t *testing.T) {
	notifier := &subscriptionNotifier{notifications: make(chan *jsonrpc.Notification, 1)}
	handler := &Handler{Notifier: notifier, Server: &Server{info: schema.Implementation{Name: "test", Version: "1"}}}
	params := schema.SubscriptionsListenRequestParams{Notifications: schema.SubscriptionFilter{}}
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	request := &jsonrpc.Request{Id: 7, Jsonrpc: jsonrpc.Version, Method: schema.MethodSubscriptionsListen, Params: raw}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan *schema.SubscriptionsListenResult, 1)
	go func() {
		result, rpcErr := handler.Listen(ctx, request)
		require.Nil(t, rpcErr)
		resultCh <- result
	}()

	select {
	case notification := <-notifier.notifications:
		require.Equal(t, schema.MethodNotificationSubscriptionsAcknowledged, notification.Method)
		var ack schema.SubscriptionsAcknowledgedNotificationParams
		require.NoError(t, json.Unmarshal(notification.Params, &ack))
		require.NotNil(t, ack.Meta)
		require.Equal(t, schema.RequestId(7), *ack.Meta.IoModelcontextprotocolSubscriptionId)
	case <-time.After(time.Second):
		t.Fatal("subscription acknowledgement was not sent")
	}
	cancel()
	select {
	case result := <-resultCh:
		require.Equal(t, schema.ResultTypeComplete, result.ResultType)
		require.Equal(t, schema.RequestId(7), result.Meta.IoModelcontextprotocolSubscriptionId)
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after cancellation")
	}
}
