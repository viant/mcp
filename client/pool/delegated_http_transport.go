// SPDX-License-Identifier: Apache-2.0
package manager

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/viant/mcp"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// DelegatedHTTPTransport describes host-owned transport restrictions below the
// manager's delegated credential layer. Base may enforce endpoint/network policy
// but must not add credentials, cookies, or request-specific authority. Credential
// attachment and metadata witnessing remain owned by the manager's resolver.
// Install the provider at manager construction; changing transport policy requires
// invalidating affected pooled clients. It does not change catalog retention.
type DelegatedHTTPTransport struct {
	Base          http.RoundTripper
	Timeout       time.Duration
	CheckRedirect func(*http.Request, []*http.Request) error
}
type DelegatedHTTPTransportProvider func(context.Context, string, string) (DelegatedHTTPTransport, error)

// WithDelegatedHTTPTransport preserves host transport policy while constructing
// authentication only after the manager decorates the resolver with its catalog
// credential witness. This avoids prebuilt auth transports bypassing that witness.
func WithDelegatedHTTPTransport(provider DelegatedHTTPTransportProvider) Option {
	return func(m *Manager) error {
		if provider == nil {
			return fmt.Errorf("delegated HTTP transport provider is required")
		}
		m.delegatedHTTPTransport = provider
		return nil
	}
}
func (m *Manager) configureDelegatedHTTPTransport(ctx context.Context, server string, options *mcp.ClientOptions) error {
	if m.delegatedHTTPTransport == nil {
		return nil
	}
	if options == nil || options.Auth == nil || options.Auth.ExternalResolver == nil {
		return fmt.Errorf("delegated HTTP transport requires installed credential resolver")
	}
	switch options.Transport.Type {
	case "streamable", "sse", "http":
	default:
		return fmt.Errorf("delegated HTTP transport requires HTTP client")
	}
	policy, err := m.delegatedHTTPTransport(ctx, server, options.Transport.URL)
	if err != nil {
		return err
	}
	if policy.Base == nil || policy.Timeout < 0 {
		return fmt.Errorf("invalid delegated HTTP transport policy")
	}
	requirement, err := options.Auth.CompileRequirement(ctx, server, options.Transport.URL)
	if err != nil {
		return err
	}
	opts := []authtransport.Option{authtransport.WithCredentialResolver(options.Auth.ExternalResolver, requirement), authtransport.WithTransport(policy.Base)}
	if options.CookieJar != nil {
		opts = append(opts, authtransport.WithCookieJar(options.CookieJar))
	}
	rt, err := authtransport.New(opts...)
	if err != nil {
		return err
	}
	options.SetAuthTransport(rt, &http.Client{Transport: observingRoundTripper{base: rt, serverName: server}, Timeout: policy.Timeout, CheckRedirect: policy.CheckRedirect, Jar: options.CookieJar})
	return nil
}
