package namespace

import "context"

// contextKey is a private key type to avoid collisions.
type contextKey struct{}

// descriptorKey is the key under which a Descriptor is stored in context.
var descriptorKey = &contextKey{}

// IntoContext stores the Descriptor in the provided context and returns the derived context.
func IntoContext(ctx context.Context, d Descriptor) context.Context {
	return context.WithValue(ctx, descriptorKey, d)
}

// FromContext retrieves a previously stored Descriptor from the context.
func FromContext(ctx context.Context) (Descriptor, bool) {
	if ctx == nil {
		return Descriptor{}, false
	}
	v := ctx.Value(descriptorKey)
	if v == nil {
		return Descriptor{}, false
	}
	if d, ok := v.(Descriptor); ok {
		return d, true
	}
	return Descriptor{}, false
}
