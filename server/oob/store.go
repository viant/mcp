package oob

import "context"

// Store is a generic backing registry for typed pendings; it is the source of
// truth mapping id -> namespace (and payload). Implementations may be in-memory
// or persistent.
type Store[T any] interface {
	Put(ctx context.Context, p Pending[T]) error
	Get(ctx context.Context, id string) (Pending[T], bool, error)
	Complete(ctx context.Context, id string) (Pending[T], bool, error)
	Cancel(ctx context.Context, id string) (Pending[T], bool, error)

	// Optional helper endpoints
	ListNamespace(ctx context.Context, namespace string) ([]Pending[T], error)
	ClearNamespace(ctx context.Context, namespace string) ([]string, error)
}
