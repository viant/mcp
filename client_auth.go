package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	authcfg "github.com/viant/mcp/client/auth/config"
)

// IsDelegated reports whether this configuration selects the delegated OAuth
// path (external resolver owns credentials; legacy interactive/BFF flows are
// disabled). Legacy configurations — no Mode, no provider references, no
// installed resolver — are never delegated.
func (c *ClientAuth) IsDelegated() bool {
	if c == nil {
		return false
	}
	if c.ExternalResolver != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(c.Mode), authcfg.ModeOAuth) &&
		(c.ProviderRef != "" || c.InlineProvider != nil)
}

// Validate checks the auth configuration for structural conflicts. Legacy
// configurations (OAuth2ConfigURL/BFF/PassUserToken only) always pass.
func (c *ClientAuth) Validate() error {
	if c == nil {
		return nil
	}
	mode := strings.TrimSpace(c.Mode)
	switch {
	case mode == "" || strings.EqualFold(mode, authcfg.ModeOAuth):
	default:
		return fmt.Errorf("auth: unsupported mode %q (expected empty or %q)", c.Mode, authcfg.ModeOAuth)
	}
	if c.ProviderRef != "" && c.InlineProvider != nil {
		return fmt.Errorf("auth: providerRef %q and inlineProvider are mutually exclusive", c.ProviderRef)
	}
	if c.ClientRef != "" && c.ProviderRef == "" && c.InlineProvider == nil {
		return fmt.Errorf("auth: clientRef %q requires providerRef or inlineProvider", c.ClientRef)
	}
	if !authcfg.ValidTokenType(c.TokenType) {
		return fmt.Errorf("auth: invalid tokenType %q (expected %q or %q)", c.TokenType, authcfg.TokenTypeAccessToken, authcfg.TokenTypeIDToken)
	}
	if !authcfg.ValidResolution(c.Resolution) {
		return fmt.Errorf("auth: invalid resolution %q (expected %q or %q)", c.Resolution, authcfg.ResolutionEager, authcfg.ResolutionChallenge)
	}
	if !authcfg.ValidReusePolicy(c.WorkspaceTokenReuse) {
		return fmt.Errorf("auth: invalid workspaceTokenReuse %q (expected %q or %q)", c.WorkspaceTokenReuse, authcfg.ReusePolicyNever, authcfg.ReusePolicyIfCompatible)
	}
	if c.InlineProvider != nil {
		if err := c.InlineProvider.Validate(); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if c.IsDelegated() {
		if c.PassUserToken != nil && *c.PassUserToken {
			return fmt.Errorf("auth: passUserToken=true is not meaningful in delegated oauth mode; use workspaceTokenReuse=%q instead", authcfg.ReusePolicyIfCompatible)
		}
		if c.BackendForFrontend != nil && *c.BackendForFrontend {
			return fmt.Errorf("auth: backendForFrontend and delegated oauth mode are mutually exclusive")
		}
		if len(c.OAuth2ConfigURL) > 0 {
			return fmt.Errorf("auth: oauth2ConfigURL and delegated oauth mode are mutually exclusive")
		}
	}
	return nil
}

// CompileRequirement compiles this configuration into the host-neutral
// credential requirement resolved before transport use. serverName labels the
// MCP definition; transportURL supplies the default resource and the origin
// check reference.
func (c *ClientAuth) CompileRequirement(ctx context.Context, serverName, transportURL string) (*authcfg.Requirement, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	requirement := &authcfg.Requirement{
		ServerName:  serverName,
		ProviderRef: c.ProviderRef,
		ClientRef:   c.ClientRef,
		Resource:    strings.TrimSpace(c.Resource),
		Scopes:      authcfg.NormalizeScopes(c.Scopes),
		TokenType:   authcfg.TokenType(c.TokenType),
		Resolution:  authcfg.Resolution(c.Resolution),
		ReusePolicy: authcfg.WorkspaceTokenReusePolicy(c.WorkspaceTokenReuse),
		Provider:    c.InlineProvider,
	}
	if requirement.TokenType == "" {
		requirement.TokenType = authcfg.TokenTypeAccessToken
	}
	if requirement.Resolution == "" {
		requirement.Resolution = authcfg.ResolutionEager
	}
	if requirement.ReusePolicy == "" {
		requirement.ReusePolicy = authcfg.ReusePolicyNever
	}
	if requirement.Resource == "" {
		requirement.Resource = transportURL
	} else if !c.AllowCrossOriginResource && transportURL != "" {
		if err := ensureSameOrigin(requirement.Resource, transportURL); err != nil {
			return nil, fmt.Errorf("auth: %w (set allowCrossOriginResource to permit)", err)
		}
	}
	if c.InlineProvider != nil {
		requirement.Issuer = authcfg.NormalizeIssuer(c.InlineProvider.Issuer)
		if requirement.ClientRef == "" {
			requirement.ClientRef = c.InlineProvider.DefaultClient
		}
	} else if c.ProviderRef != "" && c.ProviderRegistry != nil {
		provider, err := c.ProviderRegistry.ResolveProvider(ctx, c.ProviderRef)
		if err != nil {
			return nil, fmt.Errorf("auth: failed to resolve providerRef %q: %w", c.ProviderRef, err)
		}
		if _, clientName, err := provider.Client(requirement.ClientRef); err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		} else if requirement.ClientRef == "" {
			requirement.ClientRef = clientName
		}
		requirement.Issuer = authcfg.NormalizeIssuer(provider.Issuer)
	}
	return requirement, nil
}

func ensureSameOrigin(resource, transportURL string) error {
	resourceURL, err := url.Parse(resource)
	if err != nil {
		return fmt.Errorf("invalid resource %q: %w", resource, err)
	}
	endpointURL, err := url.Parse(transportURL)
	if err != nil {
		return fmt.Errorf("invalid transport URL %q: %w", transportURL, err)
	}
	if resourceURL.Scheme != endpointURL.Scheme || resourceURL.Host != endpointURL.Host {
		return fmt.Errorf("resource %q origin does not match transport %q origin", resource, transportURL)
	}
	return nil
}
