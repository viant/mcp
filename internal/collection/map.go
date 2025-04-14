package collection

import "sync"

type SyncMap[K comparable, V any] struct {
	m   map[K]V
	mux sync.RWMutex
}

func (m *SyncMap[K, V]) Get(k K) (V, bool) {
	m.mux.RLock()
	defer m.mux.RUnlock()
	v, ok := m.m[k]
	return v, ok
}

func (m *SyncMap[K, V]) Put(k K, v V) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.m[k] = v
}

func (m *SyncMap[K, V]) Delete(k K) {
	m.mux.Lock()
	defer m.mux.Unlock()
	// delete the key from the map
	if _, ok := m.m[k]; ok {
		delete(m.m, k)
	}
}

func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	for k, v := range m.m {
		// call the function with the key and value
		if !f(k, v) {
			return // stop iteration if f returns false
		}
	}
}

func NewSyncMap[K comparable, V any]() *SyncMap[K, V] {
	return &SyncMap[K, V]{m: make(map[K]V)}

}
