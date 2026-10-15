// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

// MCPAuthChangeEvent describes a delegated MCP credential change (link,
// disconnect, invalid_grant). Subscribers evict caches only — the event never
// changes EffectiveUserID, sessions or the workspace provider.
type MCPAuthChangeEvent struct {
	// Kind is one of "linked", "disconnected".
	Kind string
	// CanonicalUserID keys credential storage and resolver cooldowns.
	CanonicalUserID string
	// EffectiveUserID keys the MCP manager client pool.
	EffectiveUserID string
	ServerName      string
	ProviderRef     string
	StorageKey      string
}

// Changes notify only the owning host; no process-global listeners survive it.
func notifyMCPAuthChange(cfg *Config, event MCPAuthChangeEvent) {
	if cfg != nil && cfg.OnCredentialChange != nil {
		cfg.OnCredentialChange(event)
	}
}
