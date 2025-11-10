package oob

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory implementation of Store[T]. It is concurrency-safe
// and intended for single-process deployments or testing.
type MemoryStore[T any] struct {
	mu   sync.RWMutex
	byID map[string]Pending[T]
	byNS map[string]map[string]Pending[T]
}

func NewMemoryStore[T any]() *MemoryStore[T] {
	return &MemoryStore[T]{
		byID: make(map[string]Pending[T]),
		byNS: make(map[string]map[string]Pending[T]),
	}
}

func (s *MemoryStore[T]) Put(_ context.Context, p Pending[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[p.ID] = p
	m := s.byNS[p.Namespace]
	if m == nil {
		m = make(map[string]Pending[T])
		s.byNS[p.Namespace] = m
	}
	m[p.ID] = p
	return nil
}

func (s *MemoryStore[T]) Get(_ context.Context, id string) (Pending[T], bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	return p, ok, nil
}

func (s *MemoryStore[T]) Complete(_ context.Context, id string) (Pending[T], bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		var zero Pending[T]
		return zero, false, nil
	}
	delete(s.byID, id)
	if m := s.byNS[p.Namespace]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(s.byNS, p.Namespace)
		}
	}
	return p, true, nil
}

func (s *MemoryStore[T]) Cancel(ctx context.Context, id string) (Pending[T], bool, error) {
	// same as Complete; semantic distinction at caller
	return s.Complete(ctx, id)
}

func (s *MemoryStore[T]) ListNamespace(_ context.Context, ns string) ([]Pending[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Pending[T], 0)
	if m := s.byNS[ns]; m != nil {
		for _, v := range m {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *MemoryStore[T]) ClearNamespace(_ context.Context, ns string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0)
	if m := s.byNS[ns]; m != nil {
		for id, v := range m {
			ids = append(ids, id)
			delete(s.byID, id)
			delete(m, id)
			_ = v
		}
		delete(s.byNS, ns)
	}
	return ids, nil
}
