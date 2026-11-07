// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	authctx "github.com/viant/mcp/client/auth/link"
	"strings"
)

type resourceCatalogScope struct {
	server   string
	receiver [32]byte
}

// resourceReceiver uses trusted host context, never notification payload claims.
// An absent principal is conservative broadcast scope. Credentials and sessions
// remain in snapshot keys; invalidation follows the same owner's variants.
func (m *Manager) resourceReceiver(ctx context.Context) [32]byte {
	if canonical := authctx.CanonicalUserID(ctx); canonical != "" {
		return sha256.Sum256([]byte("canonical:" + canonical))
	}
	user := authctx.EffectiveUserID(ctx)
	if m.userIDFn != nil {
		user = m.userIDFn(ctx)
	}
	if user == "" {
		return [32]byte{}
	}
	raw, _ := json.Marshal([]string{user, authctx.EffectiveUserID(ctx), authctx.Provider(ctx)})
	return sha256.Sum256(raw)
}

// ResourceCatalogGeneration fences metadata only, independently of transport
// and tools/list generations. Native resource writes therefore cannot change
// the connection identity of their own successful action.
func (m *Manager) ResourceCatalogGeneration(ctx context.Context, server string) [2]uint64 {
	receiver := m.resourceReceiver(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resourceCatalogGenerationLocked(server, receiver)
}
func (m *Manager) resourceCatalogGenerationLocked(server string, receiver [32]byte) [2]uint64 {
	broadcast := m.resourceCatalogGeneration[resourceCatalogScope{server: server}]
	if receiver == ([32]byte{}) {
		return [2]uint64{broadcast, 0}
	}
	return [2]uint64{broadcast, m.resourceCatalogGeneration[resourceCatalogScope{server: server, receiver: receiver}]}
}

// ResourceContentGeneration fences request-local definition reuse without
// invalidating the provider's resource-list snapshot.
func (m *Manager) ResourceContentGeneration(ctx context.Context, server string) [2]uint64 {
	receiver := m.resourceReceiver(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resourceContentGenerationLocked(server, receiver)
}
func (m *Manager) resourceContentGenerationLocked(server string, receiver [32]byte) [2]uint64 {
	broadcast := m.resourceContentGeneration[resourceCatalogScope{server: server}]
	if receiver == ([32]byte{}) {
		return [2]uint64{broadcast, 0}
	}
	return [2]uint64{broadcast, m.resourceContentGeneration[resourceCatalogScope{server: server, receiver: receiver}]}
}

// OnResourceCatalogChange reports immediate native resource invalidation.
// Listeners run outside the mutex and must not perform network I/O. Refresh is
// lazy: bursts share the existing VisibleCatalog population singleflight.
func (m *Manager) OnResourceCatalogChange(listener func(string)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.resourceChangeListeners == nil {
		m.resourceChangeListeners = map[uint64]func(string){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.resourceChangeListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.resourceChangeListeners, id); m.mu.Unlock() }
}

// OnResourceUpdated observes a URI-bearing content update. List membership is
// unchanged; listeners must not perform network I/O on the notification thread.
func (m *Manager) OnResourceUpdated(listener func(string, string)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.resourceUpdatedListeners == nil {
		m.resourceUpdatedListeners = map[uint64]func(string, string){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.resourceUpdatedListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.resourceUpdatedListeners, id); m.mu.Unlock() }
}

// notificationNamesLocked reuses aliases owned by existing scoped clients.
// No configuration reads or network calls occur on the notification path.
func (m *Manager) notificationNamesLocked(server string, receiver [32]byte) map[string]bool {
	names := map[string]bool{server: true}
	for _, pool := range m.pool {
		for canonical, client := range pool {
			if client == nil || (receiver != ([32]byte{}) && client.resourceReceiver != receiver) {
				continue
			}
			if canonical != server && !client.aliases[server] {
				continue
			}
			names[canonical] = true
			for alias := range client.aliases {
				names[alias] = true
			}
		}
	}
	return names
}
func catalogUsesNames(sources map[string]bool, names map[string]bool) bool {
	if sources == nil {
		return true
	}
	for name := range names {
		if sources[name] {
			return true
		}
	}
	return false
}

// notifyResourceUpdated evicts only the named definition and its revision
// aliases. A pending load is fenced so it cannot republish after the event.
func (m *Manager) notifyResourceUpdated(server string, receiver [32]byte, public bool, uri string) {
	if uri == "" {
		m.notifyResourceCatalogChange(server, receiver, public)
		return
	}
	if public {
		receiver = [32]byte{}
	}
	m.mu.Lock()
	if m.resourceContentGeneration == nil {
		m.resourceContentGeneration = map[resourceCatalogScope]uint64{}
	}
	names := m.notificationNamesLocked(server, receiver)
	for name := range names {
		m.resourceContentGeneration[resourceCatalogScope{server: name, receiver: receiver}]++
	}
	prefix := uri + "\x00"
	for _, held := range m.visibleCatalogs {
		if !catalogUsesNames(held.sources, names) || receiver != ([32]byte{}) && held.receiver != receiver && held.receiver != ([32]byte{}) {
			continue
		}
		kept := held.entries[:0]
		for _, entry := range held.entries {
			if strings.HasPrefix(entry.key, prefix) {
				entry.invalidated = true
				continue
			}
			kept = append(kept, entry)
		}
		held.entries = kept
	}
	listeners := make([]func(string, string), 0, len(m.resourceUpdatedListeners))
	for _, listener := range m.resourceUpdatedListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for name := range names {
		for _, listener := range listeners {
			listener(name, uri)
		}
	}
}
func (m *Manager) notifyResourceCatalogChange(server string, receiver [32]byte, public bool) {
	if public {
		receiver = [32]byte{}
	}
	m.mu.Lock()
	if m.resourceCatalogGeneration == nil {
		m.resourceCatalogGeneration = map[resourceCatalogScope]uint64{}
	}
	names := m.notificationNamesLocked(server, receiver)
	for name := range names {
		m.resourceCatalogGeneration[resourceCatalogScope{server: name, receiver: receiver}]++
	}
	for key, held := range m.visibleCatalogs {
		// Missing dependency metadata is conservative, never evidence of isolation.
		affected := catalogUsesNames(held.sources, names)
		if affected && (receiver == ([32]byte{}) || held.receiver == receiver || held.receiver == ([32]byte{})) {
			delete(m.visibleCatalogs, key)
		}
	}
	listeners := make([]func(string), 0, len(m.resourceChangeListeners))
	for _, listener := range m.resourceChangeListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for name := range names {
		for _, listener := range listeners {
			listener(name)
		}
	}
}

// WithCatalogSources tags the existing snapshot store with configured server
// dependencies. It does not change the stable owner/category mutation partition.
func (m *Manager) WithCatalogSources(ctx context.Context, partition VisibleCatalogPartition, names []string) VisibleCatalogPartition {
	sources := make(map[string]bool, len(names))
	for _, name := range names {
		sources[name] = true
	}
	partition.sources = &sources
	versions := make(map[string][2]uint64, len(names))
	receiver := m.resourceReceiver(ctx)
	if partition.manager == m && partition.owner == ([32]byte{}) {
		receiver = [32]byte{}
	}
	m.mu.Lock()
	for _, name := range names {
		versions[name] = m.resourceCatalogGenerationLocked(name, receiver)
	}
	m.mu.Unlock()
	partition.sourceVersions = &versions
	return partition
}
func sameCatalogPartition(left, right VisibleCatalogPartition) bool {
	return left.manager == right.manager && left.owner == right.owner && left.category == right.category
}
