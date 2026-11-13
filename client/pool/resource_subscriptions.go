// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"time"

	"github.com/viant/mcp-protocol/schema"
)

// startResourceSubscription uses the negotiated catalog capabilities on one
// stream. No extra discovery, polling or eager catalog population runs here.
func (c *sensitiveClient) startResourceSubscription(ctx context.Context, h *changeHandler, conversation string) error {
	negotiated, err := c.Client.NegotiatedServer()
	if err != nil {
		return err
	}
	if negotiated.ProtocolVersion != schema.LatestProtocolVersion {
		c.toolListChanges = negotiated.Capabilities.Tools != nil && negotiated.Capabilities.Tools.ListChanged != nil && *negotiated.Capabilities.Tools.ListChanged
		return nil
	}
	requested := true
	filter := schema.SubscriptionFilter{}
	if resources := negotiated.Capabilities.Resources; resources != nil && resources.ListChanged != nil && *resources.ListChanged {
		filter.ResourcesListChanged = &requested
	}
	if tools := negotiated.Capabilities.Tools; tools != nil && tools.ListChanged != nil && *tools.ListChanged {
		filter.ToolsListChanged = &requested
	}
	if filter.ResourcesListChanged == nil && filter.ToolsListChanged == nil {
		return nil
	}
	stream, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.subscriptionCancel = cancel
	finished := make(chan error, 1)
	go func() {
		_, err := c.Client.Listen(stream, filter)
		finished <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case ack := <-h.subscriptionAck:
		resources := filter.ResourcesListChanged != nil && ack.Notifications.ResourcesListChanged != nil && *ack.Notifications.ResourcesListChanged
		tools := filter.ToolsListChanged != nil && ack.Notifications.ToolsListChanged != nil && *ack.Notifications.ToolsListChanged
		c.toolListChanges = tools
		if !resources && !tools {
			cancel()
		}
		if resources || tools {
			key := h.manager.poolKey(ctx, conversation)
			go func() {
				<-finished
				if stream.Err() == nil {
					c.retireLostSubscription(h, key, resources, tools)
				}
			}()
		}
		return nil
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case err := <-finished:
		cancel()
		return err
	case <-timer.C:
		cancel()
		return context.DeadlineExceeded
	}
}

// A lost standard stream retires only its actual pooled connection. It never
// retries a resource action or interprets notification data as permission.
func (c *sensitiveClient) retireLostSubscription(h *changeHandler, key string, resources, tools bool) {
	c.subscriptionLost.Store(true)
	m := h.manager
	m.mu.Lock()
	e := m.pool[key][h.server]
	var closeNow bool
	if e != nil && e.client == c {
		closeNow = m.evictEntryLocked(key, h.server, e) != nil
	} else {
		closeNow = true
	}
	m.mu.Unlock()
	if resources {
		m.notifyResourceCatalogChange(h.server, h.receiver, h.publicResources)
	}
	if tools {
		m.notifyCatalogChange(h.server)
	}
	if closeNow {
		c.Close()
	}
}
