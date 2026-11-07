// SPDX-License-Identifier: Apache-2.0
package manager

import (
	"errors"
	"fmt"
	"sync"

	mcpclient "github.com/viant/mcp/client"
)

// ErrClientCapacity reports that every capacity slot is creating a client or
// serving an RPC. Callers may retry admission; operations are never replayed.
var ErrClientCapacity = errors.New("mcp client pool capacity exhausted")

// WithMaxClients bounds this manager's pooled clients and in-flight creations across
// all principals/conversations and connections. Evicted clients still serving
// an RPC retain their slot until the RPC completes. Without this option the
// existing unlimited admission policy is preserved.
func WithMaxClients(limit int) Option {
	return func(m *Manager) error {
		if limit <= 0 {
			return fmt.Errorf("max clients must be positive")
		}
		m.maxClients = limit
		return nil
	}
}

// reserveClientSlotLocked reserves before creation starts. Reuse and creation
// singleflight must be checked first. Idle eviction uses the ordinary catalog
// invalidation and managed-client fencing path; it cannot interrupt an RPC.
func (m *Manager) reserveClientSlotLocked() (mcpclient.Interface, error) {
	if m.maxClients == 0 {
		return nil, nil
	}
	var retired mcpclient.Interface
	if m.capacityUsed >= m.maxClients {
		var oldest *entry
		var owner, server string
		for key, entries := range m.pool {
			for name, candidate := range entries {
				if candidate == nil || candidate.client == nil || candidate.active != 0 || candidate.evicted || candidate.closed || !candidate.capacityHeld {
					continue
				}
				if oldest == nil || candidate.usedAt.Before(oldest.usedAt) || candidate.usedAt.Equal(oldest.usedAt) && (key < owner || key == owner && name < server) {
					oldest, owner, server = candidate, key, name
				}
			}
		}
		if oldest == nil {
			return nil, ErrClientCapacity
		}
		// Transfer the existing slot to the new creation. The caller closes
		// the retired transport before starting the replacement factory.
		oldest.capacityHeld = false
		retired = m.evictEntryLocked(owner, server, oldest, ClientCapacityEvicted)
		return retired, nil
	}
	m.capacityUsed++
	return retired, nil
}

func (m *Manager) releaseClientSlotLocked(e *entry) {
	if e != nil && e.capacityHeld {
		e.capacityHeld = false
		m.capacityUsed--
	}
}

// A slot remains charged while an explicitly retired transport closes. This
// wrapper is used only for teardown; it does not introduce another client pool.
type capacityRetiredClient struct {
	mcpclient.Interface
	manager *Manager
	entry   *entry
	once    sync.Once
}

func (c *capacityRetiredClient) Close() {
	c.once.Do(func() {
		defer func() { c.manager.mu.Lock(); c.manager.releaseClientSlotLocked(c.entry); c.manager.mu.Unlock() }()
		closeClientBestEffort(c.Interface)
	})
}
func (m *Manager) retirementClientLocked(e *entry) mcpclient.Interface {
	if e.capacityHeld {
		return &capacityRetiredClient{Interface: e.client, manager: m, entry: e}
	}
	return e.client
}
