// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"fmt"
	mcpclient "github.com/viant/mcp/client"
	"sync"
	"sync/atomic"
)

// sensitiveClient links the actual RPC session to its callback owner without
// sending wire metadata or retaining caller credentials or payload bytes.
type sensitiveClient struct {
	*mcpclient.Client
	callback           interface{ BeginSensitivePayload() func() }
	subscriptionCancel context.CancelFunc
	closeOnce          sync.Once
	subscriptionLost   atomic.Bool
}

func (c *sensitiveClient) BeginSensitivePayload() (func(), error) {
	if c.callback == nil {
		return nil, fmt.Errorf("artifact dispatch callback fence unavailable")
	}
	return c.callback.BeginSensitivePayload(), nil
}
func (c *managedClient) BeginSensitivePayload() (func(), error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	fence, ok := client.(interface{ BeginSensitivePayload() (func(), error) })
	if !ok {
		release()
		return nil, fmt.Errorf("artifact dispatch callback fence unavailable")
	}
	end, err := fence.BeginSensitivePayload()
	if err != nil {
		release()
		return nil, err
	}
	return func() { end(); release() }, nil
}

type sensitiveSessionKey struct{}

// NewSensitiveClient owns a separate session whose callbacks stay fenced until
// the transport closes. Ordinary cached sessions are never poisoned by uploads.
func (m *Manager) NewSensitiveClient(ctx context.Context, conversation, name string) (mcpclient.Interface, error) {
	return m.newClient(context.WithValue(m.WithAuthTokenContext(ctx, name), sensitiveSessionKey{}, true), conversation, name)
}

func (c *sensitiveClient) Close() {
	c.closeOnce.Do(func() {
		if c.subscriptionCancel != nil {
			c.subscriptionCancel()
		}
		c.Client.Close()
	})
}
