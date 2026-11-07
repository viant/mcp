// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	"strings"
	"sync/atomic"
)

// OnCatalogChange subscribes host registries to upstream list-change events.
// Listeners must not perform network I/O from the transport callback.
func (m *Manager) OnCatalogChange(listener func(string)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.changeListeners == nil {
		m.changeListeners = map[uint64]func(string){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.changeListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.changeListeners, id); m.mu.Unlock() }
}

type changeHandler struct {
	protoclient.Handler
	manager         *Manager
	server          string
	receiver        [32]byte
	publicResources bool
	subscriptionAck chan schema.SubscriptionsAcknowledgedNotificationParams
	sequence        int64
	initialized     int64
}

// Forward the sensitive-session fence through the feature's catalog-change
// wrapper so uploads cannot lose callback isolation while retaining updates.
func (h *changeHandler) BeginSensitivePayload() func() {
	if fence, ok := h.Handler.(interface{ BeginSensitivePayload() func() }); ok {
		return fence.BeginSensitivePayload()
	}
	return func() {}
}

func (h *changeHandler) SetConversationID(id string) {
	if setter, ok := h.Handler.(interface{ SetConversationID(string) }); ok {
		setter.SetConversationID(id)
	}
}

func (h *changeHandler) OnNotification(ctx context.Context, n *jsonrpc.Notification) {
	if n != nil {
		switch n.Method {
		case schema.MethodNotificationSubscriptionsAcknowledged:
			if h.subscriptionAck != nil {
				var ack schema.SubscriptionsAcknowledgedNotificationParams
				if err := json.Unmarshal(n.Params, &ack); err == nil {
					select {
					case h.subscriptionAck <- ack:
					default:
					}
				}
			}
		case "notifications/resources/list_changed":
			h.manager.notifyResourceCatalogChange(h.server, h.receiver, h.publicResources)
		case "notifications/resources/updated":
			var updated schema.ResourceUpdatedNotificationParams
			if err := json.Unmarshal(n.Params, &updated); err != nil || strings.TrimSpace(updated.Uri) == "" {
				h.manager.notifyResourceCatalogChange(h.server, h.receiver, h.publicResources)
			} else {
				h.manager.notifyResourceUpdated(h.server, h.receiver, h.publicResources, updated.Uri)
			}
		case "notifications/tools/list_changed", "notifications/prompts/list_changed", "notifications/skills/list_changed":
			h.manager.notifyCatalogChange(h.server)
		}
	}
	if h.Handler != nil {
		h.Handler.OnNotification(ctx, n)
	}
}
func (h *changeHandler) Implements(method string) bool {
	return h.Handler != nil && h.Handler.Implements(method)
}
func (h *changeHandler) Init(ctx context.Context, c *schema.ClientCapabilities) {
	if atomic.AddInt64(&h.initialized, 1) > 1 {
		h.manager.notifyCatalogChange(h.server)
	}
	if h.Handler != nil {
		h.Handler.Init(ctx, c)
	}
}
func (h *changeHandler) Notify(ctx context.Context, n *jsonrpc.Notification) error {
	if h.Handler != nil {
		return h.Handler.Notify(ctx, n)
	}
	return nil
}
func (h *changeHandler) NextRequestID() jsonrpc.RequestId {
	if h.Handler != nil {
		return h.Handler.NextRequestID()
	}
	return atomic.AddInt64(&h.sequence, 1)
}
func (h *changeHandler) LastRequestID() jsonrpc.RequestId {
	if h.Handler != nil {
		return h.Handler.LastRequestID()
	}
	return atomic.LoadInt64(&h.sequence)
}
func (h *changeHandler) ListRoots(ctx context.Context, r *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.ListRoots(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client roots unavailable", nil)
}
func (h *changeHandler) CreateMessage(ctx context.Context, r *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.CreateMessage(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client sampling unavailable", nil)
}
func (h *changeHandler) Elicit(ctx context.Context, r *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.Elicit(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client elicitation unavailable", nil)
}

func (m *Manager) notifyCatalogChange(server string) {
	m.mu.Lock()
	names := m.notificationNamesLocked(server, [32]byte{})
	for name := range names {
		m.invalidateToolCatalogLocked(name)
	}
	listeners := make([]func(string), 0, len(m.changeListeners))
	for _, listener := range m.changeListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for name := range names {
		for _, listener := range listeners {
			listener(name)
		}
	}
}
