package link

import (
	"context"
	"fmt"
	"strings"

	authcfg "github.com/viant/mcp/client/auth/config"
	iauth "github.com/viant/mcp/client/auth/link/internal/authcontext"
	"github.com/viant/mcp/client/auth/link/providerregistry"
)

// MCPLinkIdentity is a trusted host's already-authenticated identity and
// session binding. It must never be decoded directly from an HTTP/MCP body.
// Hosts resolve CanonicalUserID through their authoritative user service.
type MCPLinkIdentity struct{ CanonicalUserID, EffectiveUserID, SessionID string }

// MCPLinkBackend exposes the same provider registry, PKCE/state validation,
// grant verification and refresh lifecycle to hosts with their own ingress
// and storage. The stores may be local files; no native DB is required.
type MCPLinkBackend struct {
	service  *mcpLinkService
	resolver *DelegatedCredentialResolver
}
type MCPLinkInitiation = mcpInitiateResult
type MCPLinkStatus = mcpStatusResult
type MCPLinkCompletion = mcpCallbackResult

func NewMCPLinkBackend(cfg *Config, registry *providerregistry.Registry, tokens DelegatedTokenStore, states OAuthStateStore, configs MCPAuthConfigProvider, users UserService) (*MCPLinkBackend, error) {
	if cfg == nil || newMCPStateKeyring(cfg) == nil || registry == nil || tokens == nil || states == nil || configs == nil || users == nil {
		return nil, fmt.Errorf("mcp link backend requires configured authority and stores")
	}
	lookup, ok := users.(UserByIDLookup)
	if !ok {
		return nil, fmt.Errorf("mcp link backend requires active canonical user lookup")
	}
	resolver := NewDelegatedCredentialResolver(cfg, tokens, registry, cfg.WorkspaceNamespace)
	resolver.users = lookup
	delegated := &DelegatedMCPAuth{registry: registry, resolver: resolver}
	return &MCPLinkBackend{service: newMCPLinkService(cfg, delegated, states, configs, users), resolver: resolver}, nil
}
func (b *MCPLinkBackend) context(ctx context.Context, identity MCPLinkIdentity) (context.Context, error) {
	if b == nil || ctx == nil || ctx.Err() != nil || strings.TrimSpace(identity.CanonicalUserID) == "" || strings.TrimSpace(identity.EffectiveUserID) == "" || strings.TrimSpace(identity.SessionID) == "" || !b.service.userActive(ctx, identity.CanonicalUserID) {
		return nil, fmt.Errorf("mcp link requires an authenticated active canonical user")
	}
	ctx = InjectUser(ctx, identity.EffectiveUserID)
	return iauth.WithCanonicalUserID(ctx, identity.CanonicalUserID), nil
}
func (b *MCPLinkBackend) Initiate(ctx context.Context, identity MCPLinkIdentity, server, returnURL, callbackURL string) (*MCPLinkInitiation, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !b.service.limits.initiateUser.Allow(identity.CanonicalUserID) {
		return nil, fmt.Errorf("mcp authorization rate limit")
	}
	link, err := b.service.resolveServer(ctx, server)
	if err != nil {
		return nil, err
	}
	return b.service.initiate(ctx, link, identity.CanonicalUserID, identity.SessionID, returnURL, callbackURL, false, false)
}
func (b *MCPLinkBackend) Status(ctx context.Context, identity MCPLinkIdentity, server string) (*MCPLinkStatus, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !b.service.limits.statusUser.Allow(identity.CanonicalUserID) {
		return nil, fmt.Errorf("mcp authorization rate limit")
	}
	return b.service.status(ctx, server, identity.CanonicalUserID, identity.SessionID), nil
}
func (b *MCPLinkBackend) Complete(ctx context.Context, identity MCPLinkIdentity, code, state string) (*MCPLinkCompletion, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !b.service.limits.callbackIP.Allow(identity.CanonicalUserID + "|" + mcpStateHash(state)) {
		return nil, fmt.Errorf("mcp authorization rate limit")
	}
	session := &Session{ID: identity.SessionID, UserID: identity.CanonicalUserID, Subject: identity.EffectiveUserID}
	return b.service.completeCallback(ctx, code, state, session, identity.CanonicalUserID, identity.EffectiveUserID)
}
func (b *MCPLinkBackend) Resolve(ctx context.Context, identity MCPLinkIdentity, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	return b.resolver.Resolve(ctx, requirement)
}
func (b *MCPLinkBackend) Refresh(ctx context.Context, identity MCPLinkIdentity, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	return b.resolver.Refresh(ctx, requirement)
}
func (b *MCPLinkBackend) Invalidate(ctx context.Context, identity MCPLinkIdentity, requirement authcfg.Requirement) error {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return err
	}
	return b.resolver.Invalidate(ctx, requirement)
}
