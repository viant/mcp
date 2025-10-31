package namespace

import (
	"context"
	"sync"
)

// Factory constructs a service instance. The service remains namespace-agnostic;
// isolation is achieved by keeping a distinct instance per namespace inside the resolver.
type Factory[T any] func() (T, error)

// NamespaceService resolves the caller namespace and returns a service instance scoped to it.
// It caches one instance per namespace and does not expose or leak internals.
// The service itself remains unaware of namespaces.
type NamespaceService[T any] struct {
	Provider  Provider
	Factory   Factory[T]
	instances sync.Map // map[string]T (per-namespace)
}

// Resolve computes the namespace for the provided context and returns the namespace-scoped service instance.
// The returned service should be used directly (e.g., service.ListRepositories(ctx, input)).
func (r *NamespaceService[T]) Resolve(ctx context.Context) (T, error) {
	var zero T
	if r.Provider == nil || r.Factory == nil {
		return zero, ErrMisconfigured
	}
	desc, err := r.Provider.Namespace(ctx)
	if err != nil {
		return zero, err
	}
	if inst, ok := r.instances.Load(desc.Name); ok {
		return inst.(T), nil
	}
	v, err := r.Factory()
	if err != nil {
		return zero, err
	}
	actual, _ := r.instances.LoadOrStore(desc.Name, v)
	return actual.(T), nil
}

// WithContext computes the namespace and injects the Descriptor into a derived context for optional downstream use.
func (r *NamespaceService[T]) WithContext(ctx context.Context) (context.Context, error) {
	if r.Provider == nil {
		return ctx, ErrMisconfigured
	}
	desc, err := r.Provider.Namespace(ctx)
	if err != nil {
		return ctx, err
	}
	return IntoContext(ctx, desc), nil
}

// Descriptor resolves and returns the current namespace descriptor without modifying the context.
func (r *NamespaceService[T]) Descriptor(ctx context.Context) (Descriptor, error) {
	if r.Provider == nil {
		return Descriptor{}, ErrMisconfigured
	}
	return r.Provider.Namespace(ctx)
}

// ForNamespace returns a service instance for a given namespace name.
func (r *NamespaceService[T]) ForNamespace(namespaceName string) (T, error) {
	var zero T
	if r.Factory == nil {
		return zero, ErrMisconfigured
	}
	if v, ok := r.instances.Load(namespaceName); ok {
		return v.(T), nil
	}
	v, err := r.Factory()
	if err != nil {
		return zero, err
	}
	actual, _ := r.instances.LoadOrStore(namespaceName, v)
	return actual.(T), nil
}
