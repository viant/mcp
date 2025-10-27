package transport

import (
	"net/http"

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

func WithBackendForFrontendAuth() Option {
	return func(t *RoundTripper) {
		t.useBFF = true
	}
}

func WithAuthorizationExchangeHeader(name string) Option {
	return func(t *RoundTripper) {
		t.useBFF = true
		t.bffHeader = name
	}
}

// WithCookieJar attaches a cookie jar to the auth RoundTripper so that cookies
// are applied to outbound requests and responses handled by the RoundTripper.
func WithCookieJar(jar http.CookieJar) Option {
	return func(t *RoundTripper) {
		t.jar = jar
	}
}

// WithTransport overrides the underlying HTTP RoundTripper used by the
// auth transport for probing, retries, and metadata fetches. This can be
// used in conjunction with WrapWithCookieJar to ensure cookies are applied
// to internal calls.
func WithTransport(rt http.RoundTripper) Option {
	return func(t *RoundTripper) {
		if rt != nil {
			t.transport = rt
		}
	}
}
