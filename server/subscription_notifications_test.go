package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func TestSubscriptionNotificationsRoutesAcceptedFiltersAndStops(t *testing.T) {
	sink := &subscriptionNotifier{notifications: make(chan *jsonrpc.Notification, 10)}
	router := &subscriptionNotifications{raw: sink}
	router.setLatest(true)
	ctx := context.Background()
	changed := &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: "notifications/resources/list_changed"}
	require.NoError(t, router.Notify(ctx, changed))
	require.Empty(t, sink.notifications, "latest notifications require an accepted subscription")
	enabled := true
	stop, err := router.open(ctx, 7, schema.SubscriptionFilter{ResourcesListChanged: &enabled, ResourceSubscriptions: []string{"window://studio/one"}}, &jsonrpc.Notification{Jsonrpc: jsonrpc.Version, Method: schema.MethodNotificationSubscriptionsAcknowledged})
	require.NoError(t, err)
	require.Equal(t, schema.MethodNotificationSubscriptionsAcknowledged, (<-sink.notifications).Method)
	require.NoError(t, router.Notify(ctx, changed))
	var params map[string]interface{}
	require.NoError(t, json.Unmarshal((<-sink.notifications).Params, &params))
	require.Equal(t, float64(7), params["_meta"].(map[string]interface{})["io.modelcontextprotocol/subscriptionId"])
	require.Nil(t, changed.Params, "routing must preserve caller notification")
	require.NoError(t, router.Notify(ctx, &jsonrpc.Notification{Method: "notifications/tools/list_changed"}))
	require.NoError(t, router.Notify(ctx, &jsonrpc.Notification{Method: "notifications/resources/updated", Params: json.RawMessage(`{"uri":"window://studio/other"}`)}))
	require.Empty(t, sink.notifications)
	require.NoError(t, router.Notify(ctx, &jsonrpc.Notification{Method: "notifications/resources/updated", Params: json.RawMessage(`{"uri":"window://studio/one","_meta":{"existing":true}}`)}))
	require.NoError(t, json.Unmarshal((<-sink.notifications).Params, &params))
	require.Equal(t, true, params["_meta"].(map[string]interface{})["existing"])
	stop()
	stop()
	require.NoError(t, router.Notify(ctx, changed))
	require.Empty(t, sink.notifications)
	router.setLatest(false)
	require.NoError(t, router.Notify(ctx, changed))
	require.Same(t, changed, <-sink.notifications, "legacy session notifications remain unchanged")
}
