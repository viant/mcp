package transport

import (
	"github.com/viant/mcp/protocol/client/auth/flow"
	"github.com/viant/mcp/protocol/client/auth/store"
)

type Option func(*RoundTripper)

func WithStore(store store.Store) Option {
	return func(t *RoundTripper) {
		t.store = store
	}
}

func WithAuthFlow(flow flow.AuthFlow) Option {
	return func(t *RoundTripper) {
		t.authFlow = flow
	}
}
