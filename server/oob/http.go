package oob

import (
	"context"
	"net/http"

	"github.com/viant/mcp/server/namespace"
)

// idExtractor extracts a pending id from the request.
type idExtractor func(r *http.Request) (string, error)

// context keys
type pendingKey struct{}

// IntoContext stores a Pending[T] in context.
func IntoContext[T any](ctx context.Context, p Pending[T]) context.Context {
	return context.WithValue(ctx, pendingKey{}, p)
}

// FromContext retrieves a Pending[T] from context.
func FromContext[T any](ctx context.Context) (Pending[T], bool) {
	if ctx == nil {
		var zero Pending[T]
		return zero, false
	}
	v := ctx.Value(pendingKey{})
	if v == nil {
		var zero Pending[T]
		return zero, false
	}
	p, ok := v.(Pending[T])
	return p, ok
}

// NamespaceFromPending wraps a handler to remap the request to the correct
// namespace based on the pending id stored in the provided Store. It loads the
// pending, injects a minimal namespace descriptor into context, then calls next.
func NamespaceFromPending[T any](store Store[T], extract idExtractor, next func(context.Context, Pending[T], http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := extract(r)
		if err != nil || id == "" {
			http.Error(w, "invalid pending id", http.StatusBadRequest)
			return
		}
		p, ok, err := store.Get(r.Context(), id)
		if err != nil {
			http.Error(w, "failed to load pending", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Inject a minimal descriptor. If callers need FS-friendly paths, they
		// can reconstruct them based on p.Namespace and their own policy.
		desc := namespace.Descriptor{Name: p.Namespace, IsDefault: p.Namespace == "default"}
		ctx := namespace.IntoContext(r.Context(), desc)
		ctx = IntoContext(ctx, p)
		if err := next(ctx, p, w, r.WithContext(ctx)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
}
