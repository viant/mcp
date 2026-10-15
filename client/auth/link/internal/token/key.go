package token

// Key identifies a token set for a user+provider pair.
type Key struct {
	Subject  string // user identifier (from EffectiveUserID)
	Provider string // oauth provider name (e.g. "google", "default")
}
