// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"context"

	iauth "github.com/viant/mcp/client/auth/link/internal/authcontext"
	scyauth "github.com/viant/scy/auth"
)

// InjectUser carries host-supplied user identity for the delegated credential resolver.
// External middleware (outside this module) can call this to bridge their own
// auth context into the context key that EffectiveUserID reads.
func InjectUser(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	return iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: subject})
}

// MCPAuthToken selects a single token string suitable for outbound MCP calls.
// Delegates to the internal auth package.
func MCPAuthToken(ctx context.Context, useIDToken bool) string {
	return iauth.MCPAuthToken(ctx, useIDToken)
}

// IDToken returns only the original ID token stored by trusted ingress. It
// never falls back to an access token; user-info ACL facts require the former.
func IDToken(ctx context.Context) string { return iauth.IDToken(ctx) }

// InjectTokens stores OAuth tokens in context so that MCPAuthToken and
// downstream MCP clients can forward the logged-in user's token.
// External auth middleware (outside this module) should call this after
// authenticating the user from a session or JWT.
func InjectTokens(ctx context.Context, tokens *scyauth.Token) context.Context {
	if ctx == nil || tokens == nil {
		return ctx
	}
	ctx = iauth.WithTokens(ctx, tokens)
	if tokens.AccessToken != "" {
		ctx = iauth.WithBearer(ctx, tokens.AccessToken)
	}
	if tokens.IDToken != "" {
		ctx = iauth.WithIDToken(ctx, tokens.IDToken)
	}
	return ctx
}

// CanonicalUserID reads only the host-established canonical identity.
func CanonicalUserID(ctx context.Context) string { return iauth.CanonicalUserID(ctx) }

// EffectiveUserID reads the host-established presentation identity for token partitioning.
func EffectiveUserID(ctx context.Context) string { return iauth.EffectiveUserID(ctx) }

// VerifiedProvider reads the provider identifier established by host ingress.
func VerifiedProvider(ctx context.Context) string { return iauth.Provider(ctx) }

// UserInfo carries host-provided profile identifiers. These values are token
// partitioning metadata and never substitute for an authorization provider.
type UserInfo = iauth.UserInfo

func UserFromContext(ctx context.Context) *UserInfo { return iauth.User(ctx) }
func WithUserInfo(ctx context.Context, info *UserInfo) context.Context {
	return iauth.WithUserInfo(ctx, info)
}
func Provider(ctx context.Context) string { return iauth.Provider(ctx) }
func WithProvider(ctx context.Context, value string) context.Context {
	return iauth.WithProvider(ctx, value)
}
func WithCanonicalUserID(ctx context.Context, value string) context.Context {
	return iauth.WithCanonicalUserID(ctx, value)
}
func Bearer(ctx context.Context) string { return iauth.Bearer(ctx) }
func WithBearer(ctx context.Context, value string) context.Context {
	return iauth.WithBearer(ctx, value)
}
func WithIDToken(ctx context.Context, value string) context.Context {
	return iauth.WithIDToken(ctx, value)
}
func TokensFromContext(ctx context.Context) *scyauth.Token { return iauth.TokensFromContext(ctx) }
func WithoutTokens(ctx context.Context) context.Context    { return iauth.WithoutTokens(ctx) }
