package config

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/viant/mcp-protocol/oauth2/meta"
)

// Requirement is the compiled, host-neutral description of the credential an
// MCP client needs for one server. It is derived once from client
// configuration (and optionally trusted protected-resource metadata) and then
// passed to a CredentialResolver before transport use.
type Requirement struct {
	// ServerName identifies the MCP client/server definition.
	ServerName string
	// ProviderRef references a provider registered with the host registry.
	ProviderRef string
	// ClientRef selects a client registration within the provider.
	ClientRef string
	// Issuer is the normalized OAuth issuer when known.
	Issuer string
	// Resource is the protected resource (audience) the credential must target.
	Resource string
	// Scopes are the normalized, deduplicated required scopes.
	Scopes []string
	// TokenType selects access token (default) versus ID token.
	TokenType TokenType
	// Resolution selects eager (default) versus challenge-only resolution.
	Resolution Resolution
	// ReusePolicy is forwarded to the resolver; viant/mcp does not act on it.
	ReusePolicy WorkspaceTokenReusePolicy
	// Provider carries the inline provider definition when the client was
	// configured with one instead of ProviderRef.
	Provider *OAuthProvider
	// MetadataURL records the RFC 9728 protected-resource metadata URL the
	// requirement was enriched from (challenge-mode provider learning). It is
	// informational for the resolver/host; viant/mcp never re-fetches it.
	MetadataURL string
}

// Credential is the outbound-only credential value returned by a
// CredentialResolver. viant/mcp attaches Token to outbound MCP transport
// requests and never installs it into any host identity context.
type Credential struct {
	Token       string
	TokenType   TokenType
	ExpiresAt   time.Time
	ProviderRef string
	Resource    string
	Scopes      []string
}

// Expired reports whether the credential carries an expiry in the past.
func (c *Credential) Expired(now time.Time) bool {
	if c == nil {
		return true
	}
	return !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)
}

// Verify checks that the credential is usable and that any metadata the
// resolver supplied does not contradict the requirement. Empty credential
// metadata fields are not validated. The returned error never contains the
// credential token value.
func (c *Credential) Verify(requirement *Requirement, now time.Time) error {
	if c == nil || c.Token == "" {
		return fmt.Errorf("credential has no token")
	}
	if requirement == nil {
		return nil
	}
	if c.TokenType != "" && requirement.TokenType != "" && c.TokenType != requirement.TokenType {
		return fmt.Errorf("credential tokenType %q contradicts required tokenType %q", c.TokenType, requirement.TokenType)
	}
	if c.ProviderRef != "" && requirement.ProviderRef != "" && c.ProviderRef != requirement.ProviderRef {
		return fmt.Errorf("credential providerRef %q contradicts required providerRef %q", c.ProviderRef, requirement.ProviderRef)
	}
	if c.Resource != "" && requirement.Resource != "" && !equalURL(c.Resource, requirement.Resource) {
		return fmt.Errorf("credential resource %q contradicts required resource %q", c.Resource, requirement.Resource)
	}
	if c.Expired(now) {
		return fmt.Errorf("credential expired at %s", c.ExpiresAt.Format(time.RFC3339))
	}
	if len(c.Scopes) > 0 && len(requirement.Scopes) > 0 {
		granted := map[string]bool{}
		for _, scope := range NormalizeScopes(c.Scopes) {
			granted[scope] = true
		}
		for _, scope := range requirement.Scopes {
			if !granted[scope] {
				return fmt.Errorf("credential scopes %v do not cover required scope %q", c.Scopes, scope)
			}
		}
	}
	return nil
}

// ProviderRegistry resolves OAuth provider definitions. Hosts implement it on
// top of their own configuration store; StaticRegistry offers an in-memory
// implementation.
type ProviderRegistry interface {
	// ResolveProvider returns the provider registered under ref.
	ResolveProvider(ctx context.Context, ref string) (*OAuthProvider, error)
	// MatchIssuer returns the single provider whose normalized issuer equals
	// the normalized argument; it must fail when the match is ambiguous.
	MatchIssuer(ctx context.Context, issuer string) (*OAuthProvider, error)
}

// CredentialResolver acquires, refreshes and invalidates outbound MCP
// credentials. Hosts own persistence, user identity, interactive linking and
// refresh policy; viant/mcp only calls these hooks and attaches the returned
// value to transport requests.
//
// Resolve is invoked before initialize/discovery and before every outbound
// request in eager mode (ResolutionEager); in challenge mode
// (ResolutionChallenge) it is invoked only after a 401 challenge.
//
// Refresh is invoked at most once after a 401 rejection, followed by at most
// one retry. Refresh must by contract bypass any stale access-token cache and
// mint a fresh credential while retaining the stored refresh credential:
// viant/mcp never calls Invalidate before Refresh, so an implementation must
// not depend on Invalidate to evict a stale access token.
//
// Invalidate is called only after a credential has been rejected terminally —
// a refreshed credential rejected with 401, or a challenge-mode credential
// rejected on its single retry. Implementations may drop the rejected
// credential record, including its refresh capability, when it fires.
//
// Resolve and Refresh may return *OAuthLinkRequiredError to signal that
// interactive (re-)linking is needed; viant/mcp propagates it without opening
// a browser. Any other Resolve/Refresh error is treated as ordinary/transient
// and propagates unchanged — it is never converted into link-required.
type CredentialResolver interface {
	Resolve(ctx context.Context, requirement Requirement) (*Credential, error)
	Refresh(ctx context.Context, requirement Requirement) (*Credential, error)
	Invalidate(ctx context.Context, requirement Requirement) error
}

// Clone returns a deep copy of the requirement so callers can never mutate
// the original through the returned value. It is nil-safe.
func (r *Requirement) Clone() *Requirement {
	if r == nil {
		return nil
	}
	clone := *r
	if r.Scopes != nil {
		clone.Scopes = append([]string(nil), r.Scopes...)
	}
	clone.Provider = r.Provider.Clone()
	return &clone
}

// Validate checks requirement consistency.
func (r *Requirement) Validate() error {
	if r == nil {
		return fmt.Errorf("requirement was nil")
	}
	if !ValidTokenType(string(r.TokenType)) {
		return fmt.Errorf("requirement %q: invalid tokenType %q", r.ServerName, r.TokenType)
	}
	if !ValidResolution(string(r.Resolution)) {
		return fmt.Errorf("requirement %q: invalid resolution %q", r.ServerName, r.Resolution)
	}
	if !ValidReusePolicy(string(r.ReusePolicy)) {
		return fmt.Errorf("requirement %q: invalid workspaceTokenReuse %q", r.ServerName, r.ReusePolicy)
	}
	if r.ProviderRef != "" && r.Provider != nil {
		return fmt.Errorf("requirement %q: providerRef and inline provider are mutually exclusive", r.ServerName)
	}
	if r.Provider != nil {
		if err := r.Provider.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ApplyProtectedResourceMetadata merges trusted protected-resource metadata
// into the requirement. Populated fields are cross-checked with parsed exact
// comparisons — a mismatch is a configuration error, never an opportunity to
// silently rewrite the requirement. Empty fields are filled from metadata.
func (r *Requirement) ApplyProtectedResourceMetadata(metadata *meta.ProtectedResourceMetadata) error {
	if metadata == nil {
		return fmt.Errorf("requirement %q: protected resource metadata was nil", r.ServerName)
	}
	if r.Resource != "" && metadata.Resource != "" && !equalURL(r.Resource, metadata.Resource) {
		return fmt.Errorf("requirement %q: configured resource %q does not match protected resource metadata %q",
			r.ServerName, r.Resource, metadata.Resource)
	}
	if r.Resource == "" {
		r.Resource = metadata.Resource
	}
	if r.Issuer != "" {
		if !containsNormalizedIssuer(metadata.AuthorizationServers, r.Issuer) {
			return fmt.Errorf("requirement %q: configured issuer %q is not among protected resource authorization servers %v",
				r.ServerName, r.Issuer, metadata.AuthorizationServers)
		}
	} else if len(metadata.AuthorizationServers) == 1 {
		r.Issuer = NormalizeIssuer(metadata.AuthorizationServers[0])
	}
	return nil
}

func containsNormalizedIssuer(issuers []string, issuer string) bool {
	normalized := NormalizeIssuer(issuer)
	for _, candidate := range issuers {
		if NormalizeIssuer(candidate) == normalized {
			return true
		}
	}
	return false
}

// equalURL compares two URLs by parsed exact equality (scheme, host, path,
// query); it never applies prefix or substring matching.
func equalURL(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	return leftURL.Scheme == rightURL.Scheme &&
		leftURL.Host == rightURL.Host &&
		trimTrailingSlash(leftURL.Path) == trimTrailingSlash(rightURL.Path) &&
		leftURL.RawQuery == rightURL.RawQuery
}

func trimTrailingSlash(path string) string {
	for len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	return path
}
