package providerregistry

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/mcp/client/auth/link/internal/authcontext"
	"github.com/viant/mcp/client/auth/link/provider"
)

func inlineUser(canonical, effective string) context.Context {
	return authctx.WithCanonicalUserID(authctx.WithProvider(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: effective}), "verified-workspace"), canonical)
}

func TestInlineProvidersAreCanonicalUserScopedAndConcurrent(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{}})
	alice, bob := inlineUser("alice-id", "legacy-alice"), inlineUser("bob-id", "legacy-bob")
	a, b := inlineProvider("same-id", "https://alice.fixture.test", ""), inlineProvider("same-id", "https://bob.fixture.test", "")
	a.Clients[a.DefaultClient].ConfigURL = "ref://alice"
	b.Clients[b.DefaultClient].ConfigURL = "ref://bob"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); require.NoError(t, registry.RegisterInline(alice, a)) }()
		go func() { defer wg.Done(); require.NoError(t, registry.RegisterInline(bob, b)) }()
	}
	wg.Wait()
	for _, test := range []struct {
		ctx                   context.Context
		issuer, other, config string
	}{{alice, a.Issuer, b.Issuer, "ref://alice"}, {bob, b.Issuer, a.Issuer, "ref://bob"}} {
		resolved, err := registry.ResolveProvider(test.ctx, "same-id")
		require.NoError(t, err)
		require.Equal(t, test.issuer, resolved.Issuer)
		require.Equal(t, test.config, resolved.Clients[resolved.DefaultClient].ConfigURL)
		matched, err := registry.MatchIssuer(test.ctx, test.issuer)
		require.NoError(t, err)
		require.Equal(t, "same-id", matched.ID)
		_, err = registry.MatchIssuer(test.ctx, test.other)
		require.True(t, IsNotFound(err), "other user's issuer must not enter matching")
	}
	_, err := registry.ResolveProvider(context.Background(), "same-id")
	require.True(t, IsNotFound(err), "private registration cannot become trusted startup/global data")
	require.Empty(t, registry.overlay.snapshot(""))
	require.Len(t, registry.overlay.snapshot("alice-id"), 1)
	changedAlias := inlineUser("alice-id", "another-verified-alias")
	resolved, err := registry.ResolveProvider(changedAlias, "same-id")
	require.NoError(t, err)
	require.Equal(t, a.Issuer, resolved.Issuer, "ownership derives from canonical ID, not email/subject guesses")
	resolved.Clients[resolved.DefaultClient].ConfigURL = "caller mutation"
	again, err := registry.ResolveProvider(alice, "same-id")
	require.NoError(t, err)
	require.Equal(t, "ref://alice", again.Clients[again.DefaultClient].ConfigURL)
}

func TestInlineScopePreservesFileAndTrustedStartupOperatorPrecedence(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{"file": fileProvider("file", "https://file.fixture.test")}})
	user := inlineUser("alice-id", "alice")
	aligned := inlineProvider("file", "https://file.fixture.test", "")
	require.NoError(t, registry.RegisterInline(user, aligned))
	file, err := registry.ResolveProvider(user, "file")
	require.NoError(t, err)
	require.Equal(t, "web", file.DefaultClient)
	require.Error(t, registry.RegisterInline(user, inlineProvider("file", "https://foreign.fixture.test", "")))
	global := inlineProvider("startup", "https://operator.fixture.test", "")
	require.NoError(t, registry.RegisterInline(context.Background(), global))
	private := global.Clone()
	private.Clients[private.DefaultClient].ConfigURL = "ref://private-change"
	require.NoError(t, registry.RegisterInline(user, private))
	resolved, err := registry.ResolveProvider(user, "startup")
	require.NoError(t, err)
	require.Equal(t, global.Clients[global.DefaultClient].ConfigURL, resolved.Clients[resolved.DefaultClient].ConfigURL, "trusted startup settings cannot be overwritten by a user")
	matched, err := registry.MatchIssuer(user, global.Issuer)
	require.NoError(t, err)
	require.Equal(t, "startup", matched.ID)
	_, err = registry.ResolveProvider(context.Background(), "startup")
	require.NoError(t, err)
	require.Error(t, registry.RegisterInline(user, inlineProvider("startup", "https://other.fixture.test", "")))
}

func TestInlineRegistrationCannotPromoteIncompleteAuthenticationToGlobal(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{}})
	cases := []context.Context{authctx.WithCanonicalUserID(context.Background(), "alice-id"), authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), authctx.WithProvider(context.Background(), "verified-workspace"), authctx.WithBearer(context.Background(), "synthetic-token")}
	for _, ctx := range cases {
		require.Error(t, registry.RegisterInline(ctx, inlineProvider("private", "https://private.fixture.test", "")))
	}
	_, err := registry.ResolveProvider(context.Background(), "private")
	require.True(t, IsNotFound(err))
	require.Empty(t, registry.overlay.snapshot(""))
}

func TestReadOnlyInlineLookupOwnerDoesNotCreateAuthenticationOrRegistration(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{"file": fileProvider("file", "https://file.fixture.test")}})
	alice := inlineUser("alice-id", "alice")
	require.NoError(t, registry.RegisterInline(alice, inlineProvider("private", "https://private.fixture.test", "")))
	owned := WithInlineLookupOwner(context.Background(), "alice-id")
	provider, err := registry.ResolveProvider(owned, "private")
	require.NoError(t, err)
	require.Equal(t, "private", provider.ID)
	_, err = registry.MatchIssuer(owned, "https://private.fixture.test")
	require.NoError(t, err)
	require.Empty(t, authctx.CanonicalUserID(owned))
	require.Empty(t, authctx.EffectiveUserID(owned))
	require.Empty(t, authctx.Provider(owned), "lookup owner is not authentication identity")
	require.Error(t, registry.RegisterInline(owned, inlineProvider("injected", "https://injected.fixture.test", "")))
	foreign := WithInlineLookupOwner(inlineUser("bob-id", "bob"), "alice-id")
	_, err = registry.ResolveProvider(foreign, "private")
	require.Error(t, err)
	_, err = registry.ResolveProvider(foreign, "file")
	require.Error(t, err, "file precedence cannot bypass a mismatched lookup owner")
	ctx, cancel := context.WithCancel(context.Background())
	owned = WithInlineLookupOwner(ctx, "alice-id")
	cancel()
	require.ErrorIs(t, owned.Err(), context.Canceled)
}

func TestInlineScopeAcceptsExistingTrustedHostCanonicalEffectiveIdentity(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{}})
	ctx := authctx.WithCanonicalUserID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "verified-host-user"}), "canonical-host")
	require.Empty(t, authctx.Provider(ctx))
	require.NoError(t, registry.RegisterInline(ctx, inlineProvider("host", "https://host.fixture.test", "")))
	provider, err := registry.ResolveProvider(ctx, "host")
	require.NoError(t, err)
	require.Equal(t, "host", provider.ID)
	_, err = registry.ResolveProvider(context.Background(), "host")
	require.True(t, IsNotFound(err))
}
