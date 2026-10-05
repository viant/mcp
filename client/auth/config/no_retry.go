package config

import "context"

type noRetryKey struct{}

// WithNoRetry marks a host request whose remote effect must never be replayed
// automatically, including authentication and session recovery.
func WithNoRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRetryKey{}, true)
}
func NoRetry(ctx context.Context) bool { value, _ := ctx.Value(noRetryKey{}).(bool); return value }
