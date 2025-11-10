package oob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/viant/mcp/server/namespace"
)

// CallbackBuilder returns a callback URL for the given id. The service should
// register a route that extracts this id and uses NamespaceFromPending to
// remap to the correct namespace.
type CallbackBuilder func(id string) string

// Manager coordinates creation and completion of typed pendings with namespace
// resolution and callback URL generation.
type Manager[T any] struct {
	Provider        namespace.Provider
	Store           Store[T]
	CallbackBuilder CallbackBuilder
}

// ErrMisconfigured indicates a missing Provider, Store, or CallbackBuilder.
var ErrMisconfigured = errors.New("oob: misconfigured manager (missing Provider, Store or CallbackBuilder)")

// Create resolves caller namespace, creates a new pending with a generated ID,
// stores it, and returns id and callbackURL. The Namespace field in spec is
// ignored and always set from the request context.
func (m *Manager[T]) Create(ctx context.Context, spec Spec[T]) (string, string, error) {
	if m.Provider == nil || m.Store == nil || m.CallbackBuilder == nil {
		return "", "", ErrMisconfigured
	}
	d, err := m.Provider.Namespace(ctx)
	if err != nil {
		return "", "", fmt.Errorf("resolve namespace: %w", err)
	}
	id := uuid.NewString()
	p := Pending[T]{
		ID:        id,
		Namespace: d.Name,
		Kind:      spec.Kind,
		Alias:     spec.Alias,
		Resource:  spec.Resource,
		ElicitID:  spec.ElicitID,
		CreatedAt: time.Now(),
		ExpiresAt: spec.ExpiresAt,
		Data:      spec.Data,
	}
	if err := m.Store.Put(ctx, p); err != nil {
		return "", "", err
	}
	return id, m.CallbackBuilder(id), nil
}

// Complete removes and returns the pending entry for the given id.
func (m *Manager[T]) Complete(ctx context.Context, id string) (Pending[T], error) {
	if m.Store == nil {
		var zero Pending[T]
		return zero, ErrMisconfigured
	}
	p, ok, err := m.Store.Complete(ctx, id)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, errors.New("pending not found")
	}
	return p, nil
}

// Cancel removes and returns the pending entry for the given id without signaling completion.
func (m *Manager[T]) Cancel(ctx context.Context, id string) (Pending[T], error) {
	if m.Store == nil {
		var zero Pending[T]
		return zero, ErrMisconfigured
	}
	p, ok, err := m.Store.Cancel(ctx, id)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, errors.New("pending not found")
	}
	return p, nil
}
