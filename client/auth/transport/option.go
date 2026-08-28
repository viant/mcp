package transport

import (
	"net/http"

	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp/client/auth/config"
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

// WithCredentialResolver installs an external credential resolver together
// with the compiled requirement it resolves. When installed, the RoundTripper
// becomes the sole transport-level OAuth coordinator and legacy
// interactive/browser and BFF fallbacks are disabled on this transport.
//
// In eager mode (default) it proactively resolves a credential before every
// outbound request (including initialize and discovery), attaches only the
// resolved credential, performs at most one refresh and one retry after a
// 401, and returns a typed *config.OAuthLinkRequiredError once the refreshed
// credential is rejected. Invalidate is called only after the refreshed
// credential is rejected — never before Refresh.
//
// In challenge mode (Requirement.Resolution == config.ResolutionChallenge)
// the first request is sent with Authorization explicitly removed; the
// resolver is consulted only after a 401 challenge — with a per-request
// requirement clone enriched from challenge-referenced protected-resource
// metadata pinned to the request origin — followed by at most one
// authenticated retry before the typed *config.OAuthLinkRequiredError.
//
// Ordinary Resolve/Refresh errors (transient IDP or storage failures)
// propagate unchanged; only a resolver-returned
// *config.OAuthLinkRequiredError, a missing credential, or a 401-rejected
// credential yields the typed link-required error.
func WithCredentialResolver(resolver config.CredentialResolver, requirement *config.Requirement) Option {
	return func(t *RoundTripper) {
		t.credentialResolver = resolver
		t.requirement = requirement
		t.useBFF = false
		t.disableTokenFallback = true
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
