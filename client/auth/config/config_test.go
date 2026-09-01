package config

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOAuthProvider_Validate(t *testing.T) {
	testCases := []struct {
		name        string
		provider    *OAuthProvider
		expectError bool
	}{
		{
			name:     "valid provider with default client",
			provider: &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com/", DefaultClient: "web", Clients: map[string]*OAuthClient{"web": {RedirectURI: "https://app/callback", UsePKCE: true, RefreshLead: "15m", ClockSkew: "30s"}}},
		},
		{
			name:     "valid provider without clients",
			provider: &OAuthProvider{ID: "bare", Issuer: "https://idp.example.com"},
		},
		{
			name:        "missing issuer",
			provider:    &OAuthProvider{ID: "dev6"},
			expectError: true,
		},
		{
			name:        "defaultClient not registered",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", DefaultClient: "missing"},
			expectError: true,
		},
		{
			name:        "invalid refreshLead",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"web": {RefreshLead: "abc"}}},
			expectError: true,
		},
		{
			name:        "invalid clockSkew",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"web": {ClockSkew: "xyz"}}},
			expectError: true,
		},
		{
			name:        "invalid authTimeout",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"web": {AuthTimeout: "later"}}},
			expectError: true,
		},
		{
			name:        "relative issuer",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "idp.example.com"},
			expectError: true,
		},
		{
			name:        "http issuer on non-loopback host",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "http://idp.example.com"},
			expectError: true,
		},
		{
			name:     "http issuer on loopback development host",
			provider: &OAuthProvider{ID: "dev", Issuer: "http://localhost:9090"},
		},
		{
			name:     "http issuer on 127.0.0.1",
			provider: &OAuthProvider{ID: "dev", Issuer: "http://127.0.0.1:9090"},
		},
		{
			name:        "unsupported issuer scheme",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "ftp://idp.example.com"},
			expectError: true,
		},
		{
			name:     "valid discoveryURL",
			provider: &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", DiscoveryURL: "https://idp.example.com/.well-known/openid-configuration"},
		},
		{
			name:        "http discoveryURL on non-loopback host",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", DiscoveryURL: "http://idp.example.com/.well-known/openid-configuration"},
			expectError: true,
		},
		{
			name:        "relative discoveryURL",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", DiscoveryURL: "/.well-known/openid-configuration"},
			expectError: true,
		},
		{
			name:        "redirectURI without scheme",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"web": {RedirectURI: "app/callback"}}},
			expectError: true,
		},
		{
			name:        "http redirectURI on non-loopback host",
			provider:    &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"web": {RedirectURI: "http://app.example.com/callback"}}},
			expectError: true,
		},
		{
			name:     "http redirectURI on loopback host",
			provider: &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"cli": {RedirectURI: "http://localhost/callback"}}},
		},
		{
			name:     "custom scheme redirectURI",
			provider: &OAuthProvider{ID: "dev6", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"app": {RedirectURI: "com.example.app:/callback"}}},
		},
	}
	for _, testCase := range testCases {
		err := testCase.provider.Validate()
		if testCase.expectError {
			assert.Error(t, err, testCase.name)
		} else {
			assert.NoError(t, err, testCase.name)
		}
	}
}

func TestOAuthProvider_Client(t *testing.T) {
	provider := &OAuthProvider{
		ID:            "dev6",
		Issuer:        "https://idp.example.com",
		DefaultClient: "web",
		Clients: map[string]*OAuthClient{
			"web": {RedirectURI: "https://app/callback"},
			"cli": {RedirectURI: "http://localhost/callback"},
		},
	}
	client, name, err := provider.Client("")
	assert.NoError(t, err)
	assert.Equal(t, "web", name)
	assert.Equal(t, "https://app/callback", client.RedirectURI)

	client, name, err = provider.Client("cli")
	assert.NoError(t, err)
	assert.Equal(t, "cli", name)
	assert.Equal(t, "http://localhost/callback", client.RedirectURI)

	_, _, err = provider.Client("missing")
	assert.Error(t, err)

	ambiguous := &OAuthProvider{ID: "amb", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"a": {}, "b": {}}}
	_, _, err = ambiguous.Client("")
	assert.Error(t, err, "no unambiguous default client")

	single := &OAuthProvider{ID: "one", Issuer: "https://idp.example.com", Clients: map[string]*OAuthClient{"only": {}}}
	_, name, err = single.Client("")
	assert.NoError(t, err)
	assert.Equal(t, "only", name)
}

func TestOAuthClient_Durations(t *testing.T) {
	client := &OAuthClient{RefreshLead: "15m", ClockSkew: "30s", AuthTimeout: "8m"}
	lead, err := client.RefreshLeadDuration()
	assert.NoError(t, err)
	assert.Equal(t, 15*time.Minute, lead)
	skew, err := client.ClockSkewDuration()
	assert.NoError(t, err)
	assert.Equal(t, 30*time.Second, skew)
	authTimeout, err := client.AuthTimeoutDuration()
	assert.NoError(t, err)
	assert.Equal(t, 8*time.Minute, authTimeout)

	empty := &OAuthClient{}
	lead, err = empty.RefreshLeadDuration()
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), lead)
	authTimeout, err = empty.AuthTimeoutDuration()
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Minute, authTimeout)
}

func TestNormalizeScopes(t *testing.T) {
	assert.Nil(t, NormalizeScopes(nil))
	assert.Equal(t, []string{"plan:create", "plan:edit", "plan:read"},
		NormalizeScopes([]string{" plan:read", "plan:create", "plan:edit", "plan:read", ""}))
}

func TestNormalizeIssuer(t *testing.T) {
	assert.Equal(t, "https://idp.example.com", NormalizeIssuer(" https://idp.example.com// "))
	assert.Equal(t, "https://idp.example.com", NormalizeIssuer("https://idp.example.com"))
}

func TestEnumValidation(t *testing.T) {
	assert.True(t, ValidTokenType(""))
	assert.True(t, ValidTokenType("accessToken"))
	assert.True(t, ValidTokenType("idToken"))
	assert.False(t, ValidTokenType("bearer"))

	assert.True(t, ValidResolution(""))
	assert.True(t, ValidResolution("eager"))
	assert.True(t, ValidResolution("challenge"))
	assert.False(t, ValidResolution("lazy"))

	assert.True(t, ValidReusePolicy(""))
	assert.True(t, ValidReusePolicy("never"))
	assert.True(t, ValidReusePolicy("ifCompatible"))
	assert.False(t, ValidReusePolicy("always"))
}

func TestRequirement_Validate(t *testing.T) {
	valid := &Requirement{ServerName: "dev6", ProviderRef: "adelphic-dev6", TokenType: TokenTypeAccessToken, Resolution: ResolutionEager, ReusePolicy: ReusePolicyNever}
	assert.NoError(t, valid.Validate())

	conflict := &Requirement{ServerName: "dev6", ProviderRef: "adelphic-dev6", Provider: &OAuthProvider{Issuer: "https://idp.example.com"}}
	assert.Error(t, conflict.Validate(), "providerRef and inline provider are mutually exclusive")

	badType := &Requirement{ServerName: "dev6", TokenType: "bearer"}
	assert.Error(t, badType.Validate())
}

func TestCredential_Expired(t *testing.T) {
	now := time.Now()
	var nilCredential *Credential
	assert.True(t, nilCredential.Expired(now))
	assert.False(t, (&Credential{Token: "t"}).Expired(now), "no expiry means not expired")
	assert.True(t, (&Credential{Token: "t", ExpiresAt: now.Add(-time.Minute)}).Expired(now))
	assert.False(t, (&Credential{Token: "t", ExpiresAt: now.Add(time.Minute)}).Expired(now))
}

func TestCredential_Verify(t *testing.T) {
	now := time.Now()
	requirement := &Requirement{
		ServerName:  "dev6",
		ProviderRef: "adelphic-dev6",
		Resource:    "https://mcp6.example.com/mcp",
		Scopes:      []string{"plan:read"},
		TokenType:   TokenTypeAccessToken,
	}

	assert.Error(t, (*Credential)(nil).Verify(requirement, now), "nil credential")
	assert.Error(t, (&Credential{}).Verify(requirement, now), "empty token")

	// empty metadata fields are not validated
	assert.NoError(t, (&Credential{Token: "t"}).Verify(requirement, now))
	// fully consistent metadata passes; trailing slash on the resource is tolerated
	assert.NoError(t, (&Credential{
		Token:       "t",
		TokenType:   TokenTypeAccessToken,
		ProviderRef: "adelphic-dev6",
		Resource:    "https://mcp6.example.com/mcp/",
		Scopes:      []string{"plan:read", "plan:create"},
		ExpiresAt:   now.Add(time.Hour),
	}).Verify(requirement, now))

	secret := "SECRET-TOKEN-VALUE"
	contradictions := []*Credential{
		{Token: secret, TokenType: TokenTypeIDToken},
		{Token: secret, ProviderRef: "other"},
		{Token: secret, Resource: "https://evil.example.com/mcp"},
		{Token: secret, ExpiresAt: now.Add(-time.Second)},
		{Token: secret, Scopes: []string{"other:scope"}},
	}
	for i, credential := range contradictions {
		err := credential.Verify(requirement, now)
		if assert.Error(t, err, "case %d", i) {
			assert.NotContains(t, err.Error(), secret, "case %d: token value must never be exposed", i)
		}
	}
}

func TestRequirement_Clone(t *testing.T) {
	assert.Nil(t, (*Requirement)(nil).Clone())
	original := &Requirement{
		ServerName: "dev6",
		Scopes:     []string{"plan:read"},
		Provider: &OAuthProvider{
			ID: "inline", Issuer: "https://idp.example.com",
			Clients: map[string]*OAuthClient{"web": {RedirectURI: "https://app/callback", Scopes: []string{"openid"}}},
		},
	}
	clone := original.Clone()
	clone.ServerName = "mutated"
	clone.Scopes[0] = "mutated:scope"
	clone.Provider.Issuer = "https://evil.example.com"
	clone.Provider.Clients["web"].RedirectURI = "https://evil/callback"
	clone.Provider.Clients["web"].Scopes[0] = "mutated"

	assert.Equal(t, "dev6", original.ServerName)
	assert.Equal(t, []string{"plan:read"}, original.Scopes)
	assert.Equal(t, "https://idp.example.com", original.Provider.Issuer)
	assert.Equal(t, "https://app/callback", original.Provider.Clients["web"].RedirectURI)
	assert.Equal(t, []string{"openid"}, original.Provider.Clients["web"].Scopes)
}

func TestOAuthLinkRequiredError(t *testing.T) {
	requirement := &Requirement{ServerName: "dev6", ProviderRef: "adelphic-dev6", Resource: "https://mcp6.example.com/mcp", Scopes: []string{"plan:read"}}
	cause := fmt.Errorf("refresh rejected")
	err := NewLinkRequired(requirement, cause)
	assert.Equal(t, "dev6", err.ServerName)
	assert.Equal(t, "adelphic-dev6", err.ProviderRef)
	assert.Equal(t, "https://mcp6.example.com/mcp", err.Resource)
	assert.True(t, errors.Is(err, cause), "cause preserved via Unwrap")
	assert.True(t, IsLinkRequired(err))
	assert.True(t, IsLinkRequired(fmt.Errorf("wrapped: %w", err)))
	assert.False(t, IsLinkRequired(fmt.Errorf("plain")))

	// A resolver-produced link-required error must pass through unchanged.
	original := &OAuthLinkRequiredError{ServerName: "resolver-owned", ProviderRef: "custom"}
	passedThrough := NewLinkRequired(requirement, fmt.Errorf("outer: %w", original))
	assert.Same(t, original, passedThrough)
}
