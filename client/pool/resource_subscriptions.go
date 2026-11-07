// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"time"

	"github.com/viant/mcp-protocol/schema"
)

// startResourceSubscription uses the already negotiated SDK capabilities. No
// extra discovery, polling or eager catalog population runs here.
func (c *sensitiveClient) startResourceSubscription(ctx context.Context, h *changeHandler, conversation string) error {
	negotiated, err := c.Client.NegotiatedServer()
	if err != nil {
		return err
	}
	if negotiated.ProtocolVersion != schema.LatestProtocolVersion || negotiated.Capabilities.Resources == nil || negotiated.Capabilities.Resources.ListChanged == nil || !*negotiated.Capabilities.Resources.ListChanged {
		return nil
	}
	stream, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.subscriptionCancel = cancel
	finished := make(chan error, 1)
	requested := true
	go func() {
		_, err := c.Client.Listen(stream, schema.SubscriptionFilter{ResourcesListChanged: &requested})
		finished <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case ack := <-h.subscriptionAck:
		if ack.Notifications.ResourcesListChanged == nil || !*ack.Notifications.ResourcesListChanged {
			cancel()
		}
		if ack.Notifications.ResourcesListChanged != nil && *ack.Notifications.ResourcesListChanged {
			key := h.manager.poolKey(ctx, conversation)
			go func() {
				<-finished
				if stream.Err() == nil {
					c.retireLostSubscription(h, key)
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
func (c *sensitiveClient) retireLostSubscription(h *changeHandler, key string) {
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
	m.notifyResourceCatalogChange(h.server, h.receiver, h.publicResources)
	if closeNow {
		c.Close()
	}
}
