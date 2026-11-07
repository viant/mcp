// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	mcpclient "github.com/viant/mcp/client"
)

// IsCurrentClient checks one existing locator without creating, touching or
// renewing its session. It grants no authority: callers must independently
// verify current configuration, credentials and policy before using the client.
// An entry's underlying client is immutable; replacement always evicts the
// entry. Its creation epoch is immutable too, so exact entry/managed-pointer
// identity pins both the underlying client and its session epoch.
// Registered local clients use these same owner-scoped entries and epochs.
func (m *Manager) IsCurrentClient(ctx context.Context, conversation, name string, client mcpclient.Interface) bool {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	held, ok := client.(*managedClient)
	if !ok || held == nil || held.entry == nil || held.mgr != m {
		return false
	}
	key := m.poolKey(ctx, conversation)
	if held.key != key {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if held.serverName != name && !held.entry.aliases[name] {
		return false
	}
	current := m.pool[key][held.serverName]
	return current != nil && current == held.entry && current.managed == held && current.client != nil && !current.evicted && !current.closed
}

// EvictCurrentClient retires only the exact pooled client held by this caller.
// A stale failure can never discard a newer replacement, another actor's
// session, or another source. Active calls retain their transport until they
// release it. Callers should invoke this off the request path because closing
// an idle transport can perform network I/O. It never replays a tool call.
func (m *Manager) EvictCurrentClient(ctx context.Context, conversation, name string, client mcpclient.Interface) bool {
	defer m.dispatchClientEvictions()
	if m == nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	held, ok := client.(*managedClient)
	if !ok || held == nil || held.entry == nil || held.mgr != m {
		return false
	}
	key := m.poolKey(ctx, conversation)
	if held.key != key {
		return false
	}
	var toClose mcpclient.Interface
	m.mu.Lock()
	if held.serverName != name && !held.entry.aliases[name] {
		m.mu.Unlock()
		return false
	}
	current := m.pool[key][held.serverName]
	if current == nil || current != held.entry || current.managed != held || current.client == nil || current.evicted || current.closed {
		m.mu.Unlock()
		return false
	}
	toClose = m.evictEntryLocked(key, held.serverName, current, ClientTransportEvicted)
	if m.epoch == nil {
		m.epoch = map[string]uint64{}
	}
	m.epoch[key]++
	m.mu.Unlock()
	closeClientBestEffort(toClose)
	return true
}
