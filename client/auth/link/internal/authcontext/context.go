package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	scyauth "github.com/viant/scy/auth"
)

// ctx keys use unexported distinct types to avoid collisions.
type (
	bearerKey        struct{}
	userInfoKey      struct{}
	idTokenKey       struct{}
	tokensKey        struct{}
	providerKey      struct{}
	tokenMask        struct{}
	canonicalUserKey struct{}
)

// UserInfo carries minimal identity extracted from a bearer token.
type UserInfo struct {
	Subject string
	Email   string
}

// WithTokens stores a token bundle in context.
func WithTokens(ctx context.Context, t *scyauth.Token) context.Context {
	if ctx == nil || t == nil {
		return ctx
	}
	return context.WithValue(ctx, tokensKey{}, *t)
}

// TokensFromContext returns the token bundle from context, if present.
func TokensFromContext(ctx context.Context) *scyauth.Token {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(tokensKey{}).(scyauth.Token); ok {
		return &v
	}
	return nil
}

// WithoutTokens returns a child context that masks token and downstream auth
// values inherited from its parent. It is used when stale credentials must be
// preserved in storage but must not be sent to downstream services.
func WithoutTokens(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, tokensKey{}, tokenMask{})
	ctx = context.WithValue(ctx, bearerKey{}, tokenMask{})
	return context.WithValue(ctx, idTokenKey{}, tokenMask{})
}

// MCPAuthToken selects a single token string suitable for outbound MCP calls.
// When useIDToken is true, it prefers a live IDToken and falls back to the
// access token or legacy bearer when the ID token has expired.
// When false, it prefers AccessToken and falls back to the legacy bearer key.
func MCPAuthToken(ctx context.Context, useIDToken bool) string {
	if ctx == nil {
		return ""
	}
	tb := TokensFromContext(ctx)
	if useIDToken {
		if tb != nil {
			if token := strings.TrimSpace(tb.IDToken); token != "" && !jwtTokenExpired(token, time.Now()) {
				return token
			}
		}
		if v := strings.TrimSpace(IDToken(ctx)); v != "" && !jwtTokenExpired(v, time.Now()) {
			return v
		}
		if tb != nil && strings.TrimSpace(tb.AccessToken) != "" {
			return strings.TrimSpace(tb.AccessToken)
		}
		return strings.TrimSpace(Bearer(ctx))
	}
	if tb != nil && strings.TrimSpace(tb.AccessToken) != "" {
		return strings.TrimSpace(tb.AccessToken)
	}
	if v := strings.TrimSpace(Bearer(ctx)); v != "" {
		return v
	}
	return ""
}

// jwtTokenExpired reports expiry only for JWTs carrying a numeric exp claim.
// Opaque tokens and JWTs without exp remain usable because their lifetime
// cannot be determined locally.
func jwtTokenExpired(token string, now time.Time) bool {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		ExpiresAt float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return false
	}
	return !time.Unix(int64(claims.ExpiresAt), 0).After(now.Add(30 * time.Second))
}

// WithBearer stores a raw bearer token in context.
func WithBearer(ctx context.Context, token string) context.Context {
	if ctx == nil || token == "" {
		return ctx
	}
	return context.WithValue(ctx, bearerKey{}, token)
}

// Bearer returns a raw bearer token from context, if present.
func Bearer(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(bearerKey{}).(string); ok {
		return v
	}
	return ""
}

// WithIDToken stores a raw ID token in context.
func WithIDToken(ctx context.Context, token string) context.Context {
	if ctx == nil || strings.TrimSpace(token) == "" {
		return ctx
	}
	return context.WithValue(ctx, idTokenKey{}, token)
}

// WithProvider stores an auth provider name in context.
func WithProvider(ctx context.Context, provider string) context.Context {
	if ctx == nil || strings.TrimSpace(provider) == "" {
		return ctx
	}
	return context.WithValue(ctx, providerKey{}, strings.TrimSpace(provider))
}

// Provider returns the auth provider name from context, if present.
func Provider(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(providerKey{}).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// IDToken returns a raw ID token from context, if present.
func IDToken(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(idTokenKey{}).(string); ok {
		return v
	}
	return ""
}

// WithUserInfo stores identity data in context.
func WithUserInfo(ctx context.Context, info *UserInfo) context.Context {
	if ctx == nil || info == nil {
		return ctx
	}
	return context.WithValue(ctx, userInfoKey{}, *info)
}

// User returns identity data from context when available.
func User(ctx context.Context) *UserInfo {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(userInfoKey{}).(UserInfo); ok {
		return &v
	}
	return nil
}

// WithCanonicalUserID stores the canonical Agently users.id resolved for the
// verified workspace identity. It is a persistence identity only: it never
// replaces EffectiveUserID, which stays on the workspace provider
// subject/email for the entire request lifetime.
func WithCanonicalUserID(ctx context.Context, id string) context.Context {
	if ctx == nil || strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, canonicalUserKey{}, strings.TrimSpace(id))
}

// CanonicalUserID returns the canonical Agently users.id from context, or an
// empty string when no canonical owner has been resolved. Callers persisting
// delegated (per-provider) credentials must fail closed on empty results and
// must never fall back to EffectiveUserID.
func CanonicalUserID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(canonicalUserKey{}).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// EffectiveUserID returns a stable user identifier from context (subject or email).
// Returns empty string when no identity is present.
func EffectiveUserID(ctx context.Context) string {
	if ui := User(ctx); ui != nil {
		if s := strings.TrimSpace(ui.Subject); s != "" {
			return s
		}
		if e := strings.TrimSpace(ui.Email); e != "" {
			return e
		}
	}
	return ""
}
