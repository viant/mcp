// Package config defines host-neutral OAuth configuration, requirement and
// credential types shared by MCP clients. It deliberately contains no
// host-specific user, session or storage concepts: a host application (e.g.
// a workspace runtime) implements the ProviderRegistry and CredentialResolver
// interfaces and installs them into an MCP client; viant/mcp owns protocol
// behaviour (requirement compilation, metadata discovery, credential
// attachment and 401 recovery) only.
package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Mode selects how an MCP client authenticates.
const (
	// ModeOAuth marks delegated OAuth mode: credentials are obtained through
	// an external CredentialResolver from a referenced or inline provider.
	ModeOAuth = "oauth"
)

// TokenType identifies which token kind is attached to outbound MCP requests.
type TokenType string

const (
	// TokenTypeAccessToken attaches the OAuth access token (default).
	TokenTypeAccessToken TokenType = "accessToken"
	// TokenTypeIDToken attaches the OpenID Connect ID token; it must be
	// requested explicitly.
	TokenTypeIDToken TokenType = "idToken"
)

// Resolution controls when a credential is resolved.
type Resolution string

const (
	// ResolutionEager resolves a credential before initialize/discovery and
	// before every outbound transport use (default).
	ResolutionEager Resolution = "eager"
	// ResolutionChallenge resolves a credential only after a 401 challenge.
	ResolutionChallenge Resolution = "challenge"
)

// WorkspaceTokenReusePolicy tells the host resolver whether a host (workspace)
// token may satisfy the requirement. viant/mcp never validates or forwards a
// host token itself; the policy is carried to the resolver verbatim.
type WorkspaceTokenReusePolicy string

const (
	// ReusePolicyNever forbids host-token reuse (default).
	ReusePolicyNever WorkspaceTokenReusePolicy = "never"
	// ReusePolicyIfCompatible permits reuse only after the resolver has
	// validated full issuer/audience/resource/scope/type compatibility.
	ReusePolicyIfCompatible WorkspaceTokenReusePolicy = "ifCompatible"
)

// OAuthProvider describes an OAuth authorization server and its registered
// clients. It carries configuration references only — never secret material.
type OAuthProvider struct {
	ID            string                  `yaml:"id,omitempty" json:"id,omitempty"`
	Issuer        string                  `yaml:"issuer" json:"issuer"`
	DiscoveryURL  string                  `yaml:"discoveryURL,omitempty" json:"discoveryURL,omitempty"`
	DefaultClient string                  `yaml:"defaultClient,omitempty" json:"defaultClient,omitempty"`
	Clients       map[string]*OAuthClient `yaml:"clients,omitempty" json:"clients,omitempty"`
}

// OAuthClient describes one OAuth client registration. ConfigURL references
// an external secret resource (e.g. SCY) holding client id/secret and
// endpoints; secrets never appear inline.
type OAuthClient struct {
	ConfigURL    string   `yaml:"configURL,omitempty" json:"configURL,omitempty"`
	RedirectURI  string   `yaml:"redirectURI,omitempty" json:"redirectURI,omitempty"`
	Confidential bool     `yaml:"confidential,omitempty" json:"confidential,omitempty"`
	UsePKCE      bool     `yaml:"usePKCE,omitempty" json:"usePKCE,omitempty"`
	RefreshLead  string   `yaml:"refreshLead,omitempty" json:"refreshLead,omitempty"`
	ClockSkew    string   `yaml:"clockSkew,omitempty" json:"clockSkew,omitempty"`
	Scopes       []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
}

// RefreshLeadDuration parses RefreshLead; zero when unset.
func (c *OAuthClient) RefreshLeadDuration() (time.Duration, error) {
	return parseOptionalDuration(c.RefreshLead)
}

// ClockSkewDuration parses ClockSkew; zero when unset.
func (c *OAuthClient) ClockSkewDuration() (time.Duration, error) {
	return parseOptionalDuration(c.ClockSkew)
}

func parseOptionalDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

// Validate checks provider structural correctness. Issuer and DiscoveryURL
// must be absolute HTTPS URLs (plain HTTP is permitted only for loopback
// development hosts); client redirect URIs must be well-formed absolute URIs.
func (p *OAuthProvider) Validate() error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(p.Issuer) == "" {
		return fmt.Errorf("oauth provider %q: issuer is required", p.ID)
	}
	if err := validateSecureURL(p.Issuer); err != nil {
		return fmt.Errorf("oauth provider %q: invalid issuer %q: %w", p.ID, p.Issuer, err)
	}
	if p.DiscoveryURL != "" {
		if err := validateSecureURL(p.DiscoveryURL); err != nil {
			return fmt.Errorf("oauth provider %q: invalid discoveryURL %q: %w", p.ID, p.DiscoveryURL, err)
		}
	}
	if p.DefaultClient != "" {
		if _, ok := p.Clients[p.DefaultClient]; !ok {
			return fmt.Errorf("oauth provider %q: defaultClient %q not found in clients", p.ID, p.DefaultClient)
		}
	}
	for name, client := range p.Clients {
		if client == nil {
			return fmt.Errorf("oauth provider %q: client %q is empty", p.ID, name)
		}
		if client.RedirectURI != "" {
			if err := validateRedirectURI(client.RedirectURI); err != nil {
				return fmt.Errorf("oauth provider %q: client %q: invalid redirectURI %q: %w", p.ID, name, client.RedirectURI, err)
			}
		}
		if _, err := client.RefreshLeadDuration(); err != nil {
			return fmt.Errorf("oauth provider %q: client %q: invalid refreshLead: %w", p.ID, name, err)
		}
		if _, err := client.ClockSkewDuration(); err != nil {
			return fmt.Errorf("oauth provider %q: client %q: invalid clockSkew: %w", p.ID, name, err)
		}
	}
	return nil
}

// validateSecureURL requires an absolute URL with scheme and host, and an
// https scheme except for loopback development hosts.
func validateSecureURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("must be an absolute URL with scheme and host")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return fmt.Errorf("http is permitted only for loopback development hosts")
		}
	default:
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	return nil
}

// validateRedirectURI requires a well-formed absolute URI. Custom application
// schemes (e.g. com.example.app:/callback) are permitted; plain http is
// permitted only for loopback development hosts.
func validateRedirectURI(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("must be an absolute URI with a scheme")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("http is permitted only for loopback development hosts")
	}
	if parsed.Host == "" && parsed.Path == "" && parsed.Opaque == "" {
		return fmt.Errorf("missing host or path")
	}
	return nil
}

// isLoopbackHost reports whether host is a loopback development host.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// Clone returns a deep copy of the provider. It is nil-safe.
func (p *OAuthProvider) Clone() *OAuthProvider {
	if p == nil {
		return nil
	}
	clone := *p
	if p.Clients != nil {
		clone.Clients = make(map[string]*OAuthClient, len(p.Clients))
		for name, client := range p.Clients {
			clone.Clients[name] = client.Clone()
		}
	}
	return &clone
}

// Clone returns a deep copy of the client registration. It is nil-safe.
func (c *OAuthClient) Clone() *OAuthClient {
	if c == nil {
		return nil
	}
	clone := *c
	if c.Scopes != nil {
		clone.Scopes = append([]string(nil), c.Scopes...)
	}
	return &clone
}

// Client resolves a client registration by reference, falling back to
// DefaultClient when ref is empty. It fails when the selection is ambiguous.
func (p *OAuthProvider) Client(ref string) (*OAuthClient, string, error) {
	if p == nil {
		return nil, "", fmt.Errorf("oauth provider is not configured")
	}
	if ref == "" {
		ref = p.DefaultClient
	}
	if ref == "" {
		if len(p.Clients) == 1 {
			for name, client := range p.Clients {
				return client, name, nil
			}
		}
		return nil, "", fmt.Errorf("oauth provider %q: no unambiguous default client", p.ID)
	}
	client, ok := p.Clients[ref]
	if !ok {
		return nil, "", fmt.Errorf("oauth provider %q: client %q not found", p.ID, ref)
	}
	return client, ref, nil
}

// NormalizeIssuer canonicalizes an issuer for exact comparison: it trims
// whitespace and trailing slashes. No prefix or substring matching is ever
// performed on the result.
func NormalizeIssuer(issuer string) string {
	return strings.TrimRight(strings.TrimSpace(issuer), "/")
}

// NormalizeScopes trims, deduplicates and sorts scopes for exact comparison.
func NormalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var result []string
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	sort.Strings(result)
	return result
}

// ValidTokenType reports whether value is empty or a known token type.
func ValidTokenType(value string) bool {
	switch TokenType(value) {
	case "", TokenTypeAccessToken, TokenTypeIDToken:
		return true
	}
	return false
}

// ValidResolution reports whether value is empty or a known resolution policy.
func ValidResolution(value string) bool {
	switch Resolution(value) {
	case "", ResolutionEager, ResolutionChallenge:
		return true
	}
	return false
}

// ValidReusePolicy reports whether value is empty or a known reuse policy.
func ValidReusePolicy(value string) bool {
	switch WorkspaceTokenReusePolicy(value) {
	case "", ReusePolicyNever, ReusePolicyIfCompatible:
		return true
	}
	return false
}
