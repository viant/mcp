// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	protoclient "github.com/viant/mcp-protocol/client"
	mcpclient "github.com/viant/mcp/client"
)

// installLocalNotifications attaches the same standard receiver used by remote
// transports before the new client enters the pool. Notification contexts need
// not carry identity; the verified creation scope owns invalidation.
func (m *Manager) installLocalNotifications(ctx context.Context, server string, client mcpclient.Interface) {
	setter, ok := client.(interface{ SetNotificationHandler(protoclient.Handler) })
	if !ok {
		return
	}
	var delegate protoclient.Handler
	if m.newHandler != nil {
		delegate = m.newHandler()
	}
	setter.SetNotificationHandler(&changeHandler{Handler: delegate, manager: m, server: server, receiver: m.resourceReceiver(ctx)})
}
