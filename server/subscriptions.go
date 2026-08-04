package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

type subscriptionFilterer interface {
	FilterSubscriptions(context.Context, schema.SubscriptionFilter) schema.SubscriptionFilter
}

// Listen establishes the July notification stream, acknowledges the accepted
// filter first, and remains active until its request context is cancelled.
func (h *Handler) Listen(ctx context.Context, request *jsonrpc.Request) (*schema.SubscriptionsListenResult, *jsonrpc.Error) {
	var params schema.SubscriptionsListenRequestParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("failed to parse: %v", err), request.Params)
	}
	id, ok := jsonrpc.AsRequestIntId(request.Id)
	if !ok || id <= 0 {
		return nil, jsonrpc.NewInvalidRequest("subscriptions/listen requires a positive integer request id", request.Params)
	}
	subscriptionID := schema.RequestId(id)
	accepted := schema.SubscriptionFilter{}
	if filterer, ok := h.handler.(subscriptionFilterer); ok {
		accepted = filterer.FilterSubscriptions(ctx, params.Notifications)
	}
	ackParams := schema.SubscriptionsAcknowledgedNotificationParams{
		Meta:          &schema.NotificationMetaObject{IoModelcontextprotocolSubscriptionId: &subscriptionID},
		Notifications: accepted,
	}
	raw, err := json.Marshal(ackParams)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	if h.Notifier == nil {
		return nil, jsonrpc.NewInternalError("subscriptions/listen requires a notification-capable transport", nil)
	}
	if err = h.Notifier.Notify(ctx, &jsonrpc.Notification{
		Jsonrpc: jsonrpc.Version,
		Method:  schema.MethodNotificationSubscriptionsAcknowledged,
		Params:  raw,
	}); err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}

	<-ctx.Done()
	info := h.info
	return &schema.SubscriptionsListenResult{
		Meta: schema.SubscriptionsListenResultMetaObject{
			IoModelcontextprotocolServerInfo:     &info,
			IoModelcontextprotocolSubscriptionId: subscriptionID,
		},
		ResultType: completeResultType,
	}, nil
}
