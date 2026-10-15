package token

import (
	"hash/fnv"
	"time"
)

// Refresh policy shared by request-time resolution and the background refresh
// watcher. Both must call the same ShouldRefresh decision; they must not
// maintain different thresholds.
const (
	// DefaultRefreshLead is the default lead for every registry and inline
	// provider, including the workspace provider.
	DefaultRefreshLead = 15 * time.Minute
	// MinUsefulRefreshLead is the minimum effective lead; below it a refresh
	// would race the token's own expiry.
	MinUsefulRefreshLead = 30 * time.Second
	// lifetimeClampNumerator/Denominator implement the 20% original-lifetime
	// clamp preventing immediate refresh loops for short-lived access tokens.
	lifetimeClampNumerator   = 1
	lifetimeClampDenominator = 5
)

// EffectiveRefreshLead computes the refresh lead applied to one token:
//
//	effectiveRefreshLead = min(configuredRefreshLead, originalTokenLifetime * 20%)
//	minimum useful lead  = 30 seconds
//
// configured <= 0 selects DefaultRefreshLead. lifetime <= 0 means the original
// token lifetime is unavailable: the configured lead is used unchanged and the
// caller must enforce a per-token refresh cooldown instead.
func EffectiveRefreshLead(configured, lifetime time.Duration) time.Duration {
	if configured <= 0 {
		configured = DefaultRefreshLead
	}
	effective := configured
	if lifetime > 0 {
		clamp := lifetime * lifetimeClampNumerator / lifetimeClampDenominator
		if clamp < effective {
			effective = clamp
		}
	}
	if effective < MinUsefulRefreshLead {
		effective = MinUsefulRefreshLead
	}
	return effective
}

// ShouldRefresh reports whether a token expiring at expiresAt should refresh
// now under the provider's configured lead and the token's original lifetime.
// Tokens without a known expiry never trigger refresh.
func ShouldRefresh(now, expiresAt time.Time, configured, lifetime time.Duration) bool {
	if expiresAt.IsZero() {
		return false
	}
	lead := EffectiveRefreshLead(configured, lifetime)
	return !now.Before(expiresAt.Add(-lead))
}

// RefreshJitter derives a small deterministic per-key offset (up to 10% of
// lead) so multiple pods and users do not refresh simultaneously. Determinism
// keeps request-time and background decisions for the same key identical.
func RefreshJitter(key Key, lead time.Duration) time.Duration {
	if lead <= 0 {
		return 0
	}
	window := lead / 10
	if window <= 0 {
		return 0
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(key.Subject))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(key.Provider))
	return time.Duration(hash.Sum64() % uint64(window))
}
