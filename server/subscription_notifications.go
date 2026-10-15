package server

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
)

// subscriptionNotifications routes standard July notifications on their owning
// Listen request. Legacy transports keep their session notification behavior.
type subscriptionNotifications struct {
	raw           transport.Notifier
	mu            sync.Mutex
	latest        bool
	subscriptions map[schema.RequestId]schema.SubscriptionFilter
}

func (n *subscriptionNotifications) setLatest(latest bool) {
	n.mu.Lock()
	n.latest = latest
	n.mu.Unlock()
}
func (n *subscriptionNotifications) Notify(ctx context.Context, event *jsonrpc.Notification) error {
	if n.raw == nil {
		return nil
	}
	n.mu.Lock()
	latest := n.latest
	subscriptions := make(map[schema.RequestId]schema.SubscriptionFilter, len(n.subscriptions))
	for id, filter := range n.subscriptions {
		subscriptions[id] = filter
	}
	n.mu.Unlock()
	if !latest || event == nil || !filteredNotification(event.Method) {
		return n.raw.Notify(ctx, event)
	}
	for id, filter := range subscriptions {
		if !acceptsNotification(filter, event) {
			continue
		}
		copy := *event
		params := map[string]any{}
		if len(event.Params) > 0 {
			if err := json.Unmarshal(event.Params, &params); err != nil {
				return err
			}
		}
		meta, _ := params["_meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["io.modelcontextprotocol/subscriptionId"] = id
		params["_meta"] = meta
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		copy.Params = raw
		if err = n.raw.Notify(ctx, &copy); err != nil {
			return err
		}
	}
	return nil
}
func filteredNotification(method string) bool {
	switch method {
	case "notifications/resources/list_changed", "notifications/resources/updated", "notifications/tools/list_changed", "notifications/prompts/list_changed":
		return true
	}
	return false
}
func acceptsNotification(f schema.SubscriptionFilter, n *jsonrpc.Notification) bool {
	enabled := func(p *bool) bool { return p != nil && *p }
	switch n.Method {
	case "notifications/resources/list_changed":
		return enabled(f.ResourcesListChanged)
	case "notifications/tools/list_changed":
		return enabled(f.ToolsListChanged)
	case "notifications/prompts/list_changed":
		return enabled(f.PromptsListChanged)
	case "notifications/resources/updated":
		var p struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(n.Params, &p) != nil {
			return false
		}
		for _, uri := range f.ResourceSubscriptions {
			if uri == p.URI {
				return true
			}
		}
	}
	return false
}
func (n *subscriptionNotifications) open(ctx context.Context, id schema.RequestId, filter schema.SubscriptionFilter, ack *jsonrpc.Notification) (func(), error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Serialize acknowledgement with concurrent source publication: no event can
	// precede acknowledgement or disappear between acknowledgement and insertion.
	if err := n.raw.Notify(ctx, ack); err != nil {
		return nil, err
	}
	if n.subscriptions == nil {
		n.subscriptions = map[schema.RequestId]schema.SubscriptionFilter{}
	}
	n.subscriptions[id] = filter
	var once sync.Once
	return func() { once.Do(func() { n.mu.Lock(); delete(n.subscriptions, id); n.mu.Unlock() }) }, nil
}
