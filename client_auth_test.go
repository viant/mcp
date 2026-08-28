package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	authcfg "github.com/viant/mcp/client/auth/config"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

type nopResolver struct{}

func (nopResolver) Resolve(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "token"}, nil
}
func (nopResolver) Refresh(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "token"}, nil
}
func (nopResolver) Invalidate(context.Context, authcfg.Requirement) error { return nil }

func TestClientAuth_Validate(t *testing.T) {
	inline := &authcfg.OAuthProvider{ID: "inline", Issuer: "https://idp.example.com"}
	testCases := []struct {
		name        string
		auth        *ClientAuth
		expectError string
	}{
		{name: "nil auth", auth: nil},
		{name: "empty auth", auth: &ClientAuth{}},
		{
			name: "legacy oauth2 config url",
			auth: &ClientAuth{OAuth2ConfigURL: []string{"/secret/oauth.json"}, EncryptionKey: "blowfish://default", UseIdToken: true},
		},
		{
			name: "legacy bff with pass user token",
			auth: &ClientAuth{BackendForFrontend: boolPtr(true), PassUserToken: boolPtr(true)},
		},
		{
			name: "delegated with provider ref",
			auth: &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", ClientRef: "steward-web", Scopes: []string{"plan:read"}, TokenType: "accessToken", Resolution: "eager", WorkspaceTokenReuse: "ifCompatible"},
		},
		{
			name: "delegated with inline provider",
			auth: &ClientAuth{Mode: "oauth", InlineProvider: inline},
		},
		{
			name:        "unknown mode",
			auth:        &ClientAuth{Mode: "saml"},
			expectError: "unsupported mode",
		},
		{
			name:        "providerRef and inline provider conflict",
			auth:        &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", InlineProvider: inline},
			expectError: "mutually exclusive",
		},
		{
			name:        "clientRef without provider source",
			auth:        &ClientAuth{ClientRef: "steward-web"},
			expectError: "requires providerRef or inlineProvider",
		},
		{
			name:        "invalid token type",
			auth:        &ClientAuth{TokenType: "bearer"},
			expectError: "invalid tokenType",
		},
		{
			name:        "invalid resolution",
			auth:        &ClientAuth{Resolution: "lazy"},
			expectError: "invalid resolution",
		},
		{
			name:        "invalid reuse policy",
			auth:        &ClientAuth{WorkspaceTokenReuse: "always"},
			expectError: "invalid workspaceTokenReuse",
		},
		{
			name:        "invalid inline provider",
			auth:        &ClientAuth{Mode: "oauth", InlineProvider: &authcfg.OAuthProvider{ID: "broken"}},
			expectError: "issuer is required",
		},
		{
			name:        "delegated rejects passUserToken=true",
			auth:        &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", PassUserToken: boolPtr(true)},
			expectError: "passUserToken",
		},
		{
			name: "delegated accepts explicit passUserToken=false",
			auth: &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", PassUserToken: boolPtr(false)},
		},
		{
			name:        "delegated rejects bff",
			auth:        &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", BackendForFrontend: boolPtr(true)},
			expectError: "backendForFrontend",
		},
		{
			name:        "delegated rejects legacy oauth2 config url",
			auth:        &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", OAuth2ConfigURL: []string{"/secret/oauth.json"}},
			expectError: "oauth2ConfigURL",
		},
	}
	for _, testCase := range testCases {
		err := testCase.auth.Validate()
		if testCase.expectError != "" {
			if assert.Error(t, err, testCase.name) {
				assert.Contains(t, err.Error(), testCase.expectError, testCase.name)
			}
		} else {
			assert.NoError(t, err, testCase.name)
		}
	}
}

func TestClientAuth_IsDelegated(t *testing.T) {
	assert.False(t, (*ClientAuth)(nil).IsDelegated())
	assert.False(t, (&ClientAuth{}).IsDelegated())
	assert.False(t, (&ClientAuth{OAuth2ConfigURL: []string{"x"}}).IsDelegated())
	assert.False(t, (&ClientAuth{Mode: "oauth"}).IsDelegated(), "mode oauth without provider or resolver is not delegated")
	assert.True(t, (&ClientAuth{Mode: "oauth", ProviderRef: "dev6"}).IsDelegated())
	assert.True(t, (&ClientAuth{Mode: "oauth", InlineProvider: &authcfg.OAuthProvider{Issuer: "https://idp.example.com"}}).IsDelegated())
	assert.True(t, (&ClientAuth{ExternalResolver: nopResolver{}}).IsDelegated())
}

func TestClientAuth_ShouldPassUserToken(t *testing.T) {
	// legacy behaviour preserved
	assert.True(t, (*ClientAuth)(nil).ShouldPassUserToken())
	assert.True(t, (&ClientAuth{}).ShouldPassUserToken())
	assert.True(t, (&ClientAuth{PassUserToken: boolPtr(true)}).ShouldPassUserToken())
	assert.False(t, (&ClientAuth{PassUserToken: boolPtr(false)}).ShouldPassUserToken())
	// delegated mode never forwards the host user token
	assert.False(t, (&ClientAuth{Mode: "oauth", ProviderRef: "dev6"}).ShouldPassUserToken())
	assert.False(t, (&ClientAuth{ExternalResolver: nopResolver{}}).ShouldPassUserToken())
}

func TestClientAuth_CompileRequirement(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults", func(t *testing.T) {
		auth := &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", Scopes: []string{"plan:read", "plan:create", "plan:read"}}
		requirement, err := auth.CompileRequirement(ctx, "viant-mcp-dev6", "https://mcp6.example.com/mcp")
		assert.NoError(t, err)
		assert.Equal(t, "viant-mcp-dev6", requirement.ServerName)
		assert.Equal(t, "adelphic-dev6", requirement.ProviderRef)
		assert.Equal(t, "https://mcp6.example.com/mcp", requirement.Resource, "resource defaults to transport URL")
		assert.Equal(t, []string{"plan:create", "plan:read"}, requirement.Scopes)
		assert.Equal(t, authcfg.TokenTypeAccessToken, requirement.TokenType)
		assert.Equal(t, authcfg.ResolutionEager, requirement.Resolution)
		assert.Equal(t, authcfg.ReusePolicyNever, requirement.ReusePolicy)
	})

	t.Run("cross origin resource rejected", func(t *testing.T) {
		auth := &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", Resource: "https://other.example.com/mcp"}
		_, err := auth.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "origin")
	})

	t.Run("cross origin resource allowlisted", func(t *testing.T) {
		auth := &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", Resource: "https://other.example.com/mcp", AllowCrossOriginResource: true}
		requirement, err := auth.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.NoError(t, err)
		assert.Equal(t, "https://other.example.com/mcp", requirement.Resource)
	})

	t.Run("inline provider supplies issuer and default client", func(t *testing.T) {
		auth := &ClientAuth{Mode: "oauth", InlineProvider: &authcfg.OAuthProvider{
			ID: "inline", Issuer: "https://idp.example.com/", DefaultClient: "web",
			Clients: map[string]*authcfg.OAuthClient{"web": {RedirectURI: "https://app/callback"}},
		}}
		requirement, err := auth.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.NoError(t, err)
		assert.Equal(t, "https://idp.example.com", requirement.Issuer)
		assert.Equal(t, "web", requirement.ClientRef)
		assert.NotNil(t, requirement.Provider)
	})

	t.Run("registry resolves providerRef issuer and validates clientRef", func(t *testing.T) {
		registry, err := authcfg.NewStaticRegistry(&authcfg.OAuthProvider{
			ID: "adelphic-dev6", Issuer: "https://idp-dev6.example.com/", DefaultClient: "steward-web",
			Clients: map[string]*authcfg.OAuthClient{"steward-web": {RedirectURI: "https://app/callback"}},
		})
		assert.NoError(t, err)
		auth := &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", ProviderRegistry: registry}
		requirement, err := auth.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.NoError(t, err)
		assert.Equal(t, "https://idp-dev6.example.com", requirement.Issuer)
		assert.Equal(t, "steward-web", requirement.ClientRef)

		unknown := &ClientAuth{Mode: "oauth", ProviderRef: "missing", ProviderRegistry: registry}
		_, err = unknown.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.Error(t, err)

		badClient := &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6", ClientRef: "missing", ProviderRegistry: registry}
		_, err = badClient.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.Error(t, err)
	})

	t.Run("validation errors propagate", func(t *testing.T) {
		auth := &ClientAuth{Mode: "oauth", ProviderRef: "dev6", InlineProvider: &authcfg.OAuthProvider{Issuer: "https://idp.example.com"}}
		_, err := auth.CompileRequirement(ctx, "dev6", "https://mcp6.example.com/mcp")
		assert.Error(t, err)
	})
}

func TestGetTransport_DelegatedRequiresResolver(t *testing.T) {
	options := &ClientOptions{
		Name: "dev6",
		Transport: ClientTransport{
			Type:                "streamable",
			ClientTransportHTTP: ClientTransportHTTP{URL: "https://mcp6.example.com/mcp"},
		},
		Auth: &ClientAuth{Mode: "oauth", ProviderRef: "adelphic-dev6"},
	}
	_, _, err := options.getTransport(context.Background(), &julyProtocolClientHandler{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "external credential resolver")
}

func TestGetTransport_ResolverConflictsWithInjectedTransport(t *testing.T) {
	options := &ClientOptions{
		Name: "dev6",
		Transport: ClientTransport{
			Type:                "streamable",
			ClientTransportHTTP: ClientTransportHTTP{URL: "https://mcp6.example.com/mcp"},
		},
	}
	rt, err := authtransport.New()
	assert.NoError(t, err)
	options.SetAuthTransport(rt, &http.Client{Transport: rt})
	// SetAuthTransport defaulted BFF on; clear it so validation passes and the
	// injected-transport conflict itself is exercised.
	options.Auth.BackendForFrontend = nil
	options.Auth.ExternalResolver = nopResolver{}
	_, _, err = options.getTransport(context.Background(), &julyProtocolClientHandler{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
