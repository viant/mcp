package manager

import mcpclient "github.com/viant/mcp/client"

// ClientEvictionReason distinguishes transport lifecycle from credential,
// publication or explicit shutdown. Hosts must not revive retired credentials
// or repeatedly reopen capacity victims and cause an LRU churn loop.
type ClientEvictionReason uint8

const (
	ClientRetired ClientEvictionReason = iota
	ClientIdleEvicted
	ClientCapacityEvicted
	ClientTransportEvicted
)

// Client is the exact immutable managed pointer that retired, not a secret or
// a reusable authorization decision. No actor/token material is exposed.
type ClientEviction struct {
	Server string
	Client mcpclient.Interface
	Reason ClientEvictionReason
}

// OnClientEvicted callbacks run outside the manager mutex. A callback may enqueue
// control-plane work; it must not synchronously perform network I/O. Removing a
// listener prevents future deliveries, but cannot revoke a callback in progress.
func (m *Manager) OnClientEvicted(listener func(ClientEviction)) func() {
	if m == nil || listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.clientEvictionListeners == nil {
		m.clientEvictionListeners = map[uint64]func(ClientEviction){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.clientEvictionListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.clientEvictionListeners, id); m.mu.Unlock() }
}
func (m *Manager) dispatchClientEvictions() {
	if m == nil {
		return
	}
	m.mu.Lock()
	events := m.pendingClientEvictions
	m.pendingClientEvictions = nil
	listeners := make([]func(ClientEviction), 0, len(m.clientEvictionListeners))
	for _, listener := range m.clientEvictionListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for _, event := range events {
		for _, listener := range listeners {
			listener(event)
		}
	}
}
