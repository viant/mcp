// SPDX-License-Identifier: Apache-2.0
package manager

import "context"

// AuthSelection describes the credential selected for an MCP call. Present
// describes the selected context token, not cookies or a later auth retry.
type AuthSelection struct {
	ServerName string
	Type       string
	Present    string
}

type authSelectionObserverKey struct{}

// WithAuthSelectionObserver adds a request-scoped, metadata-only observer.
func WithAuthSelectionObserver(ctx context.Context, observer func(AuthSelection)) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, authSelectionObserverKey{}, observer)
}

func reportAuthSelection(ctx context.Context, selection AuthSelection) {
	if ctx == nil {
		return
	}
	if observer, ok := ctx.Value(authSelectionObserverKey{}).(func(AuthSelection)); ok {
		observer(selection)
	}
}
