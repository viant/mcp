// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"strings"

	"time"

	scyauth "github.com/viant/scy/auth"
)

// Session represents an authenticated user session.
//
// Identity model:
//   - UserID   = canonical agently users.id when available
//   - Subject  = raw oauth/jwt subject
//   - Username = jwt.preferred_username or jwt.name — display name only
//   - Email    = jwt.email — display / contact only
//
// Identity split:
//   - sess.UserID is the canonical users.id UUID for internal persistence,
//     token storage, token refresh, and session bookkeeping.
//   - request EffectiveUserID is the provider subject/email/username used by
//     ownership and visibility filters, including created_by_user_id.
//
// Do not feed sess.EffectiveUserID() into request context unless
// created_by_user_id has been migrated to canonical users.id. Token persistence
// can use the canonical UserID, but request-scoped ownership must remain
// subject-compatible.
type Session struct {
	ID       string         `json:"id"`
	UserID   string         `json:"userId,omitempty"`
	Username string         `json:"username"`
	Email    string         `json:"email,omitempty"`
	Subject  string         `json:"subject,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Scopes   []string       `json:"scopes,omitempty"`
	Tokens   *scyauth.Token `json:"-"`
	// TransientRefreshRetryAt suppresses repeated refresh attempts/log spam
	// after a temporary token-endpoint failure. Runtime auth code persists the
	// cooldown through session metadata when a durable store is configured.
	TransientRefreshRetryAt time.Time `json:"-"`
	CreatedAt               time.Time `json:"createdAt"`
	ExpiresAt               time.Time `json:"expiresAt"`
}

// EffectiveUserID returns the canonical session identity when available.
// This is intended for session/token persistence. For request-scoped
// ownership or visibility checks, use Subject/Email/Username directly; those
// filters still compare against subject-based created_by_user_id values.
func (s *Session) EffectiveUserID() string {
	if s == nil {
		return ""
	}
	if v := strings.TrimSpace(s.UserID); v != "" {
		return v
	}
	if v := strings.TrimSpace(s.Subject); v != "" {
		return v
	}
	if v := strings.TrimSpace(s.Email); v != "" {
		return v
	}
	return strings.TrimSpace(s.Username)
}

// IsExpired returns true when the session has passed its expiry time.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}
