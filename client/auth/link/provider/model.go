// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package oauthprovider

import authcfg "github.com/viant/mcp/client/auth/config"

// Document is one provider definition file. It embeds the host-neutral
// viant/mcp provider model and adds Agently administrative controls.
type Document struct {
	authcfg.OAuthProvider `yaml:",inline" json:",inline"`

	// Disabled is the provider-level delegated-auth kill switch: a disabled
	// provider blocks reuse, refresh, initiation and callback persistence for
	// every MCP referencing it.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`

	// Introspection configures the trusted RFC 7662 endpoint used to validate
	// opaque access tokens, including client-supplied grants. Without this or
	// an explicit server-exchange TokenResponse policy, opaque grants fail closed.
	Introspection *Introspection `yaml:"introspection,omitempty" json:"introspection,omitempty"`

	// TokenResponse opts into opaque grants obtained by this server's bound
	// authorization-code exchange. It never permits raw token imports.
	TokenResponse *TokenResponse `yaml:"tokenResponse,omitempty" json:"tokenResponse,omitempty"`
}

// TokenResponse pins the trusted token endpoint and resource for providers
// without signed access tokens or complete RFC 7662 claims (for example Asana).
// SubjectPath selects a string in the token response extras, e.g. data.gid.
type TokenResponse struct {
	TokenURL    string `yaml:"tokenURL" json:"tokenURL"`
	Resource    string `yaml:"resource" json:"resource"`
	SubjectPath string `yaml:"subjectPath" json:"subjectPath"`
}

// Introspection declares the provider's authenticated token-introspection
// endpoint. The client credentials come from the referenced client's SCY
// configURL; no secret material appears here.
type Introspection struct {
	// URL is the introspection endpoint (HTTPS in production).
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// ClientRef selects the confidential client whose credentials authenticate
	// the introspection call; empty resolves the provider default client.
	ClientRef string `yaml:"clientRef,omitempty" json:"clientRef,omitempty"`
}
