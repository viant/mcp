package link

import (
	"context"
	"time"
)

// OAuthToken represents a stored OAuth token set for a user/provider pair.
// The metadata fields (Issuer, Resource, Scopes, TokenType, Subject,
// ProviderRef, ClientRef) are optional for backward compatibility with legacy
// workspace rows; delegated (per-provider MCP) tokens always populate them and
// every conversion/refresh path must preserve them.
type OAuthToken struct {
	Username     string    `json:"username"`
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"accessToken"`
	IDToken      string    `json:"idToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`

	// Issuer is the normalized OAuth issuer the token was granted by.
	Issuer string `json:"issuer,omitempty"`
	// Resource is the protected resource (audience) the token targets.
	Resource string `json:"resource,omitempty"`
	// Scopes are the granted scopes (authoritative after refresh).
	Scopes []string `json:"scopes,omitempty"`
	// TokenType records accessToken versus idToken usage intent.
	TokenType string `json:"tokenType,omitempty"`
	// Subject is the provider subject extracted from the verified token; it is
	// credential metadata only and never becomes the effective user.
	Subject string `json:"subject,omitempty"`
	// ProviderRef/ClientRef record the workspace provider registry references
	// for delegated tokens (the row provider column stores a fixed-length
	// hashed storage key).
	ProviderRef string `json:"providerRef,omitempty"`
	ClientRef   string `json:"clientRef,omitempty"`
	// IDTokenExpiresAt is the verified ID-token exp. ExpiresAt remains the
	// access-token expiry for compatibility; tokenType=idToken consumers must
	// derive validity and refresh thresholds from this field instead.
	IDTokenExpiresAt time.Time `json:"idTokenExpiresAt,omitempty"`
	// IssuedAt records when the token set was obtained (login, exchange or
	// refresh); the refresh policy uses it to derive the original selected
	// token lifetime for the 20% clamp.
	IssuedAt time.Time `json:"issuedAt,omitempty"`
}

// HasDelegatedMetadata reports whether the token carries the delegated
// credential metadata that refresh/CAS paths must not drop.
func (t *OAuthToken) HasDelegatedMetadata() bool {
	if t == nil {
		return false
	}
	return t.Issuer != "" || t.Resource != "" || len(t.Scopes) > 0 ||
		t.TokenType != "" || t.Subject != "" || t.ProviderRef != "" || t.ClientRef != ""
}

// MergeMetadataFrom copies missing metadata fields from prior. Populated
// fields on the receiver win (e.g. an authoritative refreshed scope set).
func (t *OAuthToken) MergeMetadataFrom(prior *OAuthToken) {
	if t == nil || prior == nil {
		return
	}
	if t.Issuer == "" {
		t.Issuer = prior.Issuer
	}
	if t.Resource == "" {
		t.Resource = prior.Resource
	}
	if len(t.Scopes) == 0 && len(prior.Scopes) > 0 {
		t.Scopes = append([]string(nil), prior.Scopes...)
	}
	if t.TokenType == "" {
		t.TokenType = prior.TokenType
	}
	if t.Subject == "" {
		t.Subject = prior.Subject
	}
	if t.ProviderRef == "" {
		t.ProviderRef = prior.ProviderRef
	}
	if t.ClientRef == "" {
		t.ClientRef = prior.ClientRef
	}
	if t.IssuedAt.IsZero() {
		t.IssuedAt = prior.IssuedAt
	}
	// The ID-token expiry belongs to one specific ID token value: carry it
	// over only when the receiver retained exactly the prior ID token. A
	// dropped or rotated ID token must never resurrect a stale expiry.
	if t.IDTokenExpiresAt.IsZero() && t.IDToken != "" && t.IDToken == prior.IDToken {
		t.IDTokenExpiresAt = prior.IDTokenExpiresAt
	}
}

// TokenStore abstracts encrypted OAuth token persistence.
// Implementations may use scy-backed secrets, database storage, etc.
type TokenStore interface {
	Get(ctx context.Context, username, provider string) (*OAuthToken, error)
	Put(ctx context.Context, token *OAuthToken) error
	Delete(ctx context.Context, username, provider string) error

	// TryAcquireRefreshLease atomically attempts to acquire a distributed lease
	// for refreshing the token identified by (username, provider).
	TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (version int64, acquired bool, err error)

	// ReleaseRefreshLease releases a previously acquired lease.
	ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error

	// CASPut atomically updates the token only if the current version matches
	// expectedVersion and the lease is held by owner.
	CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (swapped bool, err error)
}

// ExpiringTokenScanner is an optional extension of TokenStore for implementations
// that can efficiently query the store for tokens expiring before a given horizon.
// It is used by the background refresh watcher to proactively refresh tokens for
// users who are idle (no active in-memory session) before they expire.
// TokenStore implementations that do not embed a queryable DB may omit this.
type ExpiringTokenScanner interface {
	// ScanExpiring returns all stored tokens whose expiry is before horizon
	// and that carry a refresh token. Only tokens that can actually be
	// refreshed need to be returned.
	ScanExpiring(ctx context.Context, horizon time.Time) ([]*OAuthToken, error)
}
