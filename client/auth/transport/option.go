package transport

import (
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/scy/auth/flow"
)

type Option func(*RoundTripper)

// WithStore sets store
func WithStore(store store.Store) Option {
	return func(t *RoundTripper) {
		t.store = store
	}
}

// WithAuthFlow sets auth flow
func WithAuthFlow(flow flow.AuthFlow) Option {
	return func(t *RoundTripper) {
		t.authFlow = flow
	}
}

// WithGlobalResource sets global resource
func WithGlobalResource(global *authorization.Authorization) Option {
	return func(t *RoundTripper) {
		t.Global = global
	}
}
