package link

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DelegatedProviderKeyPrefix marks provider column values holding delegated
// (per-MCP-provider) tokens rather than workspace provider names.
const DelegatedProviderKeyPrefix = "mcp:v1:"

// DelegatedProviderKeyLength is the fixed length of a delegated storage key:
// len("mcp:v1:") + 64 hex characters of an untruncated SHA-256 digest.
const DelegatedProviderKeyLength = len(DelegatedProviderKeyPrefix) + sha256.Size*2

// DelegatedProviderStorageKey derives the fixed-length, globally unambiguous
// user_oauth_token.provider value for a delegated provider reference:
//
//	mcp:v1:<hex sha256(workspaceNamespace + NUL + providerRef)>
//
// The digest is never truncated. workspaceNamespace is an immutable configured
// identifier; changing it requires an explicit token-key migration.
func DelegatedProviderStorageKey(workspaceNamespace, providerRef string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workspaceNamespace) + "\x00" + strings.TrimSpace(providerRef)))
	return DelegatedProviderKeyPrefix + hex.EncodeToString(sum[:])
}

// IsDelegatedProviderKey reports whether a provider column value is a
// delegated storage key. Delegated rows require exact lookup and per-provider
// broker routing; they must never be served by workspace fallback or sent to
// the workspace refresh broker.
func IsDelegatedProviderKey(provider string) bool {
	provider = strings.TrimSpace(provider)
	return strings.HasPrefix(provider, DelegatedProviderKeyPrefix) &&
		len(provider) == DelegatedProviderKeyLength
}

// CanonicalWorkspaceProvider normalizes trusted workspace provider aliases
// ("", "jwt", "oauth", "default") to the configured workspace provider name.
// Delegated storage keys and unknown provider names pass through unchanged.
func CanonicalWorkspaceProvider(cfg *Config, provider string) string {
	provider = strings.TrimSpace(provider)
	if IsDelegatedProviderKey(provider) {
		return provider
	}
	configured := "oauth"
	if cfg != nil && cfg.OAuth != nil {
		if name := strings.TrimSpace(cfg.OAuth.Name); name != "" {
			configured = name
		}
	}
	switch strings.ToLower(provider) {
	case "", "jwt", "oauth", "default":
		return configured
	}
	if strings.EqualFold(provider, configured) {
		return configured
	}
	return provider
}

// IsWorkspaceProviderAlias reports whether provider names the workspace
// identity provider (directly or through a trusted legacy alias).
func IsWorkspaceProviderAlias(cfg *Config, provider string) bool {
	provider = strings.TrimSpace(provider)
	if IsDelegatedProviderKey(provider) {
		return false
	}
	switch strings.ToLower(provider) {
	case "", "jwt", "oauth", "default", "local":
		return true
	}
	if cfg != nil && cfg.OAuth != nil {
		if name := strings.TrimSpace(cfg.OAuth.Name); name != "" && strings.EqualFold(provider, name) {
			return true
		}
	}
	return false
}
