package link

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	iauth "github.com/viant/mcp/client/auth/link/internal/authcontext"
	"github.com/viant/mcp/client/auth/link/provider"
	"github.com/viant/mcp/client/auth/link/providerregistry"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// --- fakes ---

type fakeDelegatedStore struct {
	mu      sync.Mutex
	rows    map[string]*OAuthToken // userID|provider
	version map[string]int64
	leased  map[string]string
	deletes []string
	puts    int
	casPuts int
}

func newFakeDelegatedStore() *fakeDelegatedStore {
	return &fakeDelegatedStore{rows: map[string]*OAuthToken{}, version: map[string]int64{}, leased: map[string]string{}}
}

func (f *fakeDelegatedStore) key(userID, provider string) string { return userID + "|" + provider }

func (f *fakeDelegatedStore) seed(tok *OAuthToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := *tok
	f.rows[f.key(tok.Username, tok.Provider)] = &clone
}

func (f *fakeDelegatedStore) GetExact(ctx context.Context, userID, provider string) (*OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[f.key(userID, provider)]
	if !ok {
		return nil, nil
	}
	clone := *row
	return &clone, nil
}

func (f *fakeDelegatedStore) Put(ctx context.Context, token *OAuthToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	clone := *token
	f.rows[f.key(token.Username, token.Provider)] = &clone
	return nil
}

func (f *fakeDelegatedStore) Delete(ctx context.Context, userID, provider string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(userID, provider)
	f.deletes = append(f.deletes, key)
	delete(f.rows, key)
	return nil
}

func (f *fakeDelegatedStore) TryAcquireRefreshLease(ctx context.Context, userID, provider, owner string, ttl time.Duration) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(userID, provider)
	if current, held := f.leased[key]; held && current != owner {
		return 0, false, nil
	}
	f.leased[key] = owner
	return f.version[key], true, nil
}

func (f *fakeDelegatedStore) ReleaseRefreshLease(ctx context.Context, userID, provider, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.leased, f.key(userID, provider))
	return nil
}

func (f *fakeDelegatedStore) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.casPuts++
	key := f.key(token.Username, token.Provider)
	if f.version[key] != expectedVersion {
		return false, nil
	}
	clone := *token
	f.rows[key] = &clone
	f.version[key]++
	delete(f.leased, key)
	return true, nil
}

type fakeProviderLoader struct {
	docs map[string]*oauthprovider.Document
}

func (f *fakeProviderLoader) List(ctx context.Context) ([]string, error) {
	names := make([]string, 0, len(f.docs))
	for name := range f.docs {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeProviderLoader) Load(ctx context.Context, name string) (*oauthprovider.Document, error) {
	doc, ok := f.docs[name]
	if !ok {
		return nil, fmt.Errorf("not found: %s", name)
	}
	clone := *doc
	return &clone, nil
}

func dev6ProviderDoc(disabled bool) *oauthprovider.Document {
	return &oauthprovider.Document{
		OAuthProvider: authcfg.OAuthProvider{
			ID:            "adelphic-dev6",
			Issuer:        "https://idp-dev6.example.com/",
			DefaultClient: "steward-web",
			Clients: map[string]*authcfg.OAuthClient{
				"steward-web": {
					ConfigURL:   "scy://dev6-client",
					RedirectURI: "https://steward.example.com/v1/api/auth/mcp/callback",
					UsePKCE:     true,
					RefreshLead: "15m",
				},
			},
		},
		Disabled: disabled,
	}
}

func dev6Requirement() authcfg.Requirement {
	return authcfg.Requirement{
		ServerName:  "viant-mcp-dev6",
		ProviderRef: "adelphic-dev6",
		ClientRef:   "steward-web",
		Issuer:      "https://idp-dev6.example.com",
		Resource:    "https://mcp6.example.com/mcp",
		Scopes:      []string{"plan:create", "plan:edit", "plan:read"},
		TokenType:   authcfg.TokenTypeAccessToken,
		Resolution:  authcfg.ResolutionEager,
		ReusePolicy: authcfg.ReusePolicyNever,
	}
}

func newTestResolver(t *testing.T, store *fakeDelegatedStore, docs ...*oauthprovider.Document) *DelegatedCredentialResolver {
	t.Helper()
	loader := &fakeProviderLoader{docs: map[string]*oauthprovider.Document{}}
	for _, doc := range docs {
		loader.docs[doc.ID] = doc
	}
	registry := providerregistry.NewWithLoader(loader)
	cfg := &Config{OAuth: &OAuth{Name: "corp-idp"}, WorkspaceNamespace: "test-ns"}
	resolver := NewDelegatedCredentialResolver(cfg, store, registry, "test-ns")
	resolver.loadClientConfig = func(ctx context.Context, configURL string) (*oauth2.Config, error) {
		return &oauth2.Config{
			ClientID: "steward-web",
			Endpoint: oauth2.Endpoint{TokenURL: "https://idp-dev6.example.com/token"},
		}, nil
	}
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		t.Fatalf("unexpected token endpoint call")
		return nil, nil
	}
	return resolver
}

func delegatedCtx(canonicalID string) context.Context {
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "subject@corp"})
	ctx = iauth.WithProvider(ctx, "corp-idp")
	if canonicalID != "" {
		ctx = iauth.WithCanonicalUserID(ctx, canonicalID)
	}
	return ctx
}

func dev6StorageKey() string { return DelegatedProviderStorageKey("test-ns", "adelphic-dev6") }

func seededDev6Token(expiresAt time.Time) *OAuthToken {
	return &OAuthToken{
		Username:     "uuid-1",
		Provider:     dev6StorageKey(),
		AccessToken:  "dev6-access",
		RefreshToken: "dev6-refresh",
		ExpiresAt:    expiresAt,
		Issuer:       "https://idp-dev6.example.com",
		Resource:     "https://mcp6.example.com/mcp",
		Scopes:       []string{"plan:create", "plan:edit", "plan:read"},
		TokenType:    "accessToken",
		Subject:      "dev6-subject",
		ProviderRef:  "adelphic-dev6",
		ClientRef:    "steward-web",
	}
}

// unsafeJWT builds an unsigned JWT-shaped token for claim parsing tests. The
// resolver treats context tokens as already verified by upstream middleware.
func unsafeJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

// --- tests ---

func TestResolveFailsClosedWithoutCanonicalUser(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	requirement := dev6Requirement()

	// No identity at all.
	if _, err := resolver.Resolve(context.Background(), requirement); err == nil {
		t.Fatalf("expected error without workspace user")
	}
	// Effective user without canonical owner: fail closed, no link-required.
	_, err := resolver.Resolve(delegatedCtx(""), requirement)
	if err == nil || authcfg.IsLinkRequired(err) {
		t.Fatalf("missing canonical owner must fail closed with a hard error, got %v", err)
	}
}

func TestResolveMissingTokenReturnsLinkRequired(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("expected link-required, got %v", err)
	}
	var linkErr *authcfg.OAuthLinkRequiredError
	if ok := authcfg.IsLinkRequired(err); ok {
		linkErr, _ = err.(*authcfg.OAuthLinkRequiredError)
	}
	if linkErr == nil || linkErr.ProviderRef != "adelphic-dev6" || linkErr.ServerName != "viant-mcp-dev6" {
		t.Fatalf("link-required must carry requirement identity: %+v", linkErr)
	}
}

func TestResolveMissingTokenNeverFallsBackToWorkspaceToken(t *testing.T) {
	store := newFakeDelegatedStore()
	// Seed a workspace-provider row for the same user under the workspace name.
	store.seed(&OAuthToken{Username: "uuid-1", Provider: "corp-idp", AccessToken: "workspace-token"})
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	ctx := delegatedCtx("uuid-1")
	// Also inject workspace tokens into the context.
	ctx = iauth.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{AccessToken: "workspace-bearer"}})
	_, err := resolver.Resolve(ctx, dev6Requirement()) // ReusePolicyNever
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("a missing delegated token must produce link-required, got %v", err)
	}
}

func TestResolveReturnsStoredCredentialWithoutContextMutation(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(2 * time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	ctx := delegatedCtx("uuid-1")
	ctx = iauth.WithBearer(ctx, "workspace-bearer")

	credential, err := resolver.Resolve(ctx, dev6Requirement())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if credential.Token != "dev6-access" {
		t.Fatalf("credential token = %q", credential.Token)
	}
	if credential.TokenType != authcfg.TokenTypeAccessToken {
		t.Fatalf("credential type = %q", credential.TokenType)
	}
	if credential.ProviderRef != "adelphic-dev6" || credential.Resource != "https://mcp6.example.com/mcp" {
		t.Fatalf("credential metadata = %+v", credential)
	}
	// The parent auth context is unchanged: effective user, workspace
	// provider and bearer are exactly as before, and the delegated token
	// never appears through authctx accessors.
	if got := iauth.EffectiveUserID(ctx); got != "subject@corp" {
		t.Fatalf("EffectiveUserID mutated: %q", got)
	}
	if got := iauth.Provider(ctx); got != "corp-idp" {
		t.Fatalf("workspace provider mutated: %q", got)
	}
	if got := iauth.Bearer(ctx); got != "workspace-bearer" {
		t.Fatalf("workspace bearer mutated: %q", got)
	}
	if iauth.Bearer(ctx) == credential.Token || iauth.IDToken(ctx) == credential.Token {
		t.Fatalf("delegated token leaked into auth context")
	}
}

func TestResolveIDTokenRequirementNeverFallsBackToAccessToken(t *testing.T) {
	store := newFakeDelegatedStore()
	tok := seededDev6Token(time.Now().Add(2 * time.Hour))
	tok.IDToken = "" // only access token stored
	store.seed(tok)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	requirement := dev6Requirement()
	requirement.TokenType = authcfg.TokenTypeIDToken
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("idToken requirement with access-only credential must require linking, got %v", err)
	}
}

func TestResolveRefreshesExpiringTokenPreservingMetadata(t *testing.T) {
	store := newFakeDelegatedStore()
	// Expires within the 15m lead → due for refresh.
	store.seed(seededDev6Token(time.Now().Add(5 * time.Minute)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	var refreshCalls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshCalls++
		if base.RefreshToken != "dev6-refresh" {
			t.Fatalf("refresh must use the stored refresh token, got %q", base.RefreshToken)
		}
		if resource != "https://mcp6.example.com/mcp" {
			t.Fatalf("refresh must target the stored resource, got %q", resource)
		}
		if config.Endpoint.TokenURL != "https://idp-dev6.example.com/token" {
			t.Fatalf("refresh must use the provider's exact token endpoint, got %q", config.Endpoint.TokenURL)
		}
		// Provider omits a rotated refresh token.
		return &oauth2.Token{AccessToken: "fresh-access", Expiry: time.Now().Add(time.Hour)}, nil
	}

	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if credential.Token != "fresh-access" {
		t.Fatalf("credential token = %q", credential.Token)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted == nil || persisted.AccessToken != "fresh-access" {
		t.Fatalf("refresh must CAS-persist the new token: %+v", persisted)
	}
	if persisted.RefreshToken != "dev6-refresh" {
		t.Fatalf("omitted rotation must preserve the previous refresh token, got %q", persisted.RefreshToken)
	}
	if persisted.Issuer == "" || persisted.Resource == "" || len(persisted.Scopes) != 3 ||
		persisted.Subject != "dev6-subject" || persisted.ProviderRef != "adelphic-dev6" || persisted.ClientRef != "steward-web" {
		t.Fatalf("refresh dropped stored metadata: %+v", persisted)
	}
	if store.casPuts != 1 {
		t.Fatalf("expected exactly one CAS write, got %d", store.casPuts)
	}
}

func TestResolveInvalidGrantDeletesOnlyThisProvider(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(time.Minute)))
	store.seed(&OAuthToken{Username: "uuid-1", Provider: "corp-idp", AccessToken: "workspace"})
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return nil, &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
	}

	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("invalid_grant must produce link-required, got %v", err)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row != nil {
		t.Fatalf("invalid_grant must delete the delegated row")
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", "corp-idp"); row == nil {
		t.Fatalf("invalid_grant must not touch other provider rows")
	}
}

func TestResolveTransientRefreshFailureKeepsUsableToken(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(5 * time.Minute)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return nil, fmt.Errorf("token endpoint timeout")
	}

	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if err != nil {
		t.Fatalf("transient failure with valid token must not fail: %v", err)
	}
	if credential.Token != "dev6-access" {
		t.Fatalf("expected current access token, got %q", credential.Token)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row == nil {
		t.Fatalf("transient failure must preserve credentials")
	}
	// Cooldown prevents an immediate repeated endpoint call.
	if !resolver.inCooldown("uuid-1", dev6StorageKey()) {
		t.Fatalf("transient failure must set the per-token cooldown")
	}
}

func TestResolveExpiredTokenTransientRefreshFailureRequiresRelink(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(-time.Minute)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return nil, fmt.Errorf("invalid character 'Ã' looking for beginning of value")
	}

	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("an unusable token whose refresh fails must require relinking, got %v", err)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row == nil {
		t.Fatal("a transient refresh failure must preserve the stored credential for diagnostics/retry")
	}
}

func TestForceRefreshFailureRequiresRelink(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return nil, fmt.Errorf("token endpoint unavailable")
	}

	_, err := resolver.Refresh(delegatedCtx("uuid-1"), dev6Requirement())
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("a provider-rejected credential whose forced refresh fails must require relinking, got %v", err)
	}
}

func TestResolveScopeNarrowingPersistsAndRequiresRelink(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(5 * time.Minute)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshed := &oauth2.Token{AccessToken: "narrowed-access", Expiry: time.Now().Add(time.Hour)}
		return refreshed.WithExtra(map[string]interface{}{"scope": "plan:read"}), nil
	}

	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("insufficient narrowed scopes must require relinking, got %v", err)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted == nil {
		t.Fatalf("narrowed token must be retained for compatible consumers")
	}
	if len(persisted.Scopes) != 1 || persisted.Scopes[0] != "plan:read" {
		t.Fatalf("returned refresh scope must be authoritative, got %v", persisted.Scopes)
	}
}

func TestResolveUnknownProviderNeverTouchesStore(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	requirement := dev6Requirement()
	requirement.ProviderRef = "unknown-provider"
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if err == nil || authcfg.IsLinkRequired(err) {
		t.Fatalf("unknown provider must be a hard configuration error, got %v", err)
	}
	if !providerregistry.IsNotFound(err) {
		t.Fatalf("expected provider not-found, got %v", err)
	}
	if len(store.deletes) != 0 || store.puts != 0 || store.casPuts != 0 {
		t.Fatalf("unknown provider must not mutate the store")
	}
}

func TestResolveDisabledProviderKillSwitch(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(2 * time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(true))
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !providerregistry.IsDisabled(err) {
		t.Fatalf("disabled provider must return provider_disabled, got %v", err)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row == nil {
		t.Fatalf("kill switch must not delete credentials")
	}
}

func TestResolveGlobalKillSwitch(t *testing.T) {
	t.Setenv(providerregistry.GlobalKillSwitchEnv, "1")
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(2 * time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if !providerregistry.IsDisabled(err) {
		t.Fatalf("global kill switch must return provider_disabled, got %v", err)
	}
}

func TestWorkspaceTokenReuseIfCompatible(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	requirement := dev6Requirement()
	requirement.ReusePolicy = authcfg.ReusePolicyIfCompatible

	expiry := time.Now().Add(time.Hour)
	compatible := unsafeJWT(t, map[string]interface{}{
		"iss":   "https://idp-dev6.example.com/",
		"aud":   []string{"https://mcp6.example.com/mcp"},
		"scope": "plan:create plan:edit plan:read openid",
		"exp":   float64(expiry.Unix()),
	})
	ctx := delegatedCtx("uuid-1")
	ctx = iauth.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{AccessToken: compatible, Expiry: expiry}})

	credential, err := resolver.Resolve(ctx, requirement)
	if err != nil {
		t.Fatalf("compatible workspace token must be reused: %v", err)
	}
	if credential.Token != compatible {
		t.Fatalf("expected workspace token reuse")
	}
}

func TestWorkspaceTokenReuseRejectsIncompatible(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	requirement := dev6Requirement()
	requirement.ReusePolicy = authcfg.ReusePolicyIfCompatible
	expiry := time.Now().Add(time.Hour)

	cases := map[string]map[string]interface{}{
		"wrong issuer": {
			"iss":   "https://workspace-idp.example.com",
			"aud":   []string{"https://mcp6.example.com/mcp"},
			"scope": "plan:create plan:edit plan:read",
			"exp":   float64(expiry.Unix()),
		},
		"wrong audience suffix": {
			"iss":   "https://idp-dev6.example.com",
			"aud":   []string{"https://mcp6.example.com/mcp2"},
			"scope": "plan:create plan:edit plan:read",
			"exp":   float64(expiry.Unix()),
		},
		"missing scopes": {
			"iss":   "https://idp-dev6.example.com",
			"aud":   []string{"https://mcp6.example.com/mcp"},
			"scope": "plan:read",
			"exp":   float64(expiry.Unix()),
		},
		"expired": {
			"iss":   "https://idp-dev6.example.com",
			"aud":   []string{"https://mcp6.example.com/mcp"},
			"scope": "plan:create plan:edit plan:read",
			"exp":   float64(time.Now().Add(-time.Minute).Unix()),
		},
	}
	for name, claims := range cases {
		ctx := delegatedCtx("uuid-1")
		ctx = iauth.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{AccessToken: unsafeJWT(t, claims), Expiry: expiry}})
		_, err := resolver.Resolve(ctx, requirement)
		if !authcfg.IsLinkRequired(err) {
			t.Fatalf("%s: incompatible workspace token must not be reused (err=%v)", name, err)
		}
	}

	// Opaque tokens fail closed.
	ctx := delegatedCtx("uuid-1")
	ctx = iauth.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{AccessToken: "opaque-value", Expiry: expiry}})
	if _, err := resolver.Resolve(ctx, requirement); !authcfg.IsLinkRequired(err) {
		t.Fatalf("opaque workspace token must not be reused, got %v", err)
	}
}

func TestInvalidateDeletesOnlyExactRow(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(time.Hour)))
	store.seed(&OAuthToken{Username: "uuid-1", Provider: "corp-idp", AccessToken: "workspace"})
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))

	if err := resolver.Invalidate(delegatedCtx("uuid-1"), dev6Requirement()); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row != nil {
		t.Fatalf("expected delegated row deleted")
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", "corp-idp"); row == nil {
		t.Fatalf("workspace row must survive delegated invalidation")
	}
}

func TestInvalidateSkipsUnknownProviderAndMissingCanonical(t *testing.T) {
	store := newFakeDelegatedStore()
	store.seed(seededDev6Token(time.Now().Add(time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))

	requirement := dev6Requirement()
	requirement.ProviderRef = "unknown-provider"
	if err := resolver.Invalidate(delegatedCtx("uuid-1"), requirement); err != nil {
		t.Fatalf("invalidate unknown provider must be a safe no-op: %v", err)
	}
	if err := resolver.Invalidate(delegatedCtx(""), dev6Requirement()); err != nil {
		t.Fatalf("invalidate without canonical user must be a safe no-op: %v", err)
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row == nil {
		t.Fatalf("unsafe invalidation deleted a credential")
	}
}

func TestRefreshMintsFreshCredential(t *testing.T) {
	store := newFakeDelegatedStore()
	// Far from expiry: Resolve would not refresh, but Refresh must.
	store.seed(seededDev6Token(time.Now().Add(2 * time.Hour)))
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	var refreshCalls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshCalls++
		return &oauth2.Token{AccessToken: "minted", RefreshToken: "rotated", Expiry: time.Now().Add(time.Hour)}, nil
	}
	credential, err := resolver.Refresh(delegatedCtx("uuid-1"), dev6Requirement())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshCalls != 1 || credential.Token != "minted" {
		t.Fatalf("refresh must mint through the provider (calls=%d token=%q)", refreshCalls, credential.Token)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted.RefreshToken != "rotated" {
		t.Fatalf("rotated refresh token must persist, got %q", persisted.RefreshToken)
	}
}

func TestRefreshStoredDelegatedTokenSafety(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))

	// Malformed metadata: storage key does not match providerRef derivation.
	bad := seededDev6Token(time.Now().Add(time.Minute))
	bad.ProviderRef = "other-ref"
	store.seed(bad)
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), bad); err == nil {
		t.Fatalf("mismatched storage key must be reported")
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey()); row == nil {
		t.Fatalf("malformed metadata must never mutate the row")
	}

	// Unknown provider: reported, not mutated.
	unknown := seededDev6Token(time.Now().Add(time.Minute))
	unknown.ProviderRef = "ghost"
	unknown.Provider = DelegatedProviderStorageKey("test-ns", "ghost")
	store.seed(unknown)
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), unknown); err == nil {
		t.Fatalf("unknown provider must be reported")
	}
	if row, _ := store.GetExact(context.Background(), "uuid-1", unknown.Provider); row == nil {
		t.Fatalf("unknown provider must never mutate the row")
	}

	// No refresh token: never sent to the token endpoint.
	noRefresh := seededDev6Token(time.Now().Add(time.Minute))
	noRefresh.RefreshToken = ""
	store.seed(noRefresh)
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		t.Fatalf("token without refresh token must not reach the endpoint")
		return nil, nil
	}
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), noRefresh); err != nil {
		t.Fatalf("no-refresh-token row must be skipped silently: %v", err)
	}

	// Not yet due: no endpoint call.
	fresh := seededDev6Token(time.Now().Add(2 * time.Hour))
	store.seed(fresh)
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), fresh); err != nil {
		t.Fatalf("fresh row must be skipped: %v", err)
	}
}

func TestRefreshStoredDelegatedTokenRefreshesDueRow(t *testing.T) {
	store := newFakeDelegatedStore()
	due := seededDev6Token(time.Now().Add(5 * time.Minute))
	store.seed(due)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	var calls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		calls++
		if config.Endpoint.TokenURL != "https://idp-dev6.example.com/token" {
			t.Fatalf("background refresh must use the provider's exact endpoint")
		}
		return &oauth2.Token{AccessToken: "bg-fresh", Expiry: time.Now().Add(time.Hour)}, nil
	}
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), due); err != nil {
		t.Fatalf("background refresh: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one endpoint call, got %d", calls)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted.AccessToken != "bg-fresh" || persisted.ProviderRef != "adelphic-dev6" {
		t.Fatalf("background refresh must persist token with metadata: %+v", persisted)
	}
}

func TestRefreshStoredDelegatedTokenAdoptsRotatedTokenAfterWaiting(t *testing.T) {
	store := newFakeDelegatedStore()
	base := time.Now().Truncate(time.Second)
	stale := seededDev6Token(base.Add(5 * time.Minute))
	stale.IssuedAt = base.Add(-55 * time.Minute)
	store.seed(stale)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.now = func() time.Time { return base }

	var submitted []string
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, token *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		submitted = append(submitted, token.RefreshToken)
		n := len(submitted)
		return &oauth2.Token{
			AccessToken:  fmt.Sprintf("access-%d", n),
			RefreshToken: fmt.Sprintf("refresh-%d", n),
			Expiry:       resolver.now().Add(time.Hour),
		}, nil
	}

	// The first caller rotates the original one-time refresh token.
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), stale); err != nil {
		t.Fatalf("first background refresh: %v", err)
	}
	// This caller still holds the pre-refresh snapshot. It must adopt the
	// protected current row instead of replaying the consumed token.
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), stale); err != nil {
		t.Fatalf("stale background refresh: %v", err)
	}
	if len(submitted) != 1 || submitted[0] != "dev6-refresh" {
		t.Fatalf("stale waiter replayed a refresh token: submitted=%v", submitted)
	}

	// At the next refresh window, even a stale scan item must submit the last
	// persisted rotation and durably store the next one.
	resolver.now = func() time.Time { return base.Add(59 * time.Minute) }
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), stale); err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if len(submitted) != 2 || submitted[1] != "refresh-1" {
		t.Fatalf("second rotation did not use the persisted token: submitted=%v", submitted)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted == nil || persisted.RefreshToken != "refresh-2" || persisted.AccessToken != "access-2" {
		t.Fatalf("second rotation was not persisted: %+v", persisted)
	}
}

// --- selected-token (idToken) expiry semantics ---

func idTokenValue(t *testing.T, issued, expires time.Time) string {
	t.Helper()
	return unsafeJWT(t, map[string]interface{}{
		"iss": "https://idp-dev6.example.com",
		"sub": "dev6-subject",
		"iat": float64(issued.Unix()),
		"exp": float64(expires.Unix()),
	})
}

// TestResolveIDTokenExpiryDrivesRefreshAndCredential proves that for
// tokenType=idToken the refresh threshold and Credential.ExpiresAt derive from
// the verified ID-token exp, not the access-token expiry.
func TestResolveIDTokenExpiryDrivesRefreshAndCredential(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	// Access token is far from expiry; the ID token is inside the 15m lead.
	tok := seededDev6Token(now.Add(4 * time.Hour))
	tok.TokenType = "idToken"
	tok.IDToken = idTokenValue(t, now.Add(-55*time.Minute), now.Add(5*time.Minute))
	tok.IDTokenExpiresAt = now.Add(5 * time.Minute)
	store.seed(tok)

	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	newIDExpiry := now.Add(time.Hour).Truncate(time.Second)
	newIDToken := idTokenValue(t, now, newIDExpiry)
	var refreshCalls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshCalls++
		refreshed := &oauth2.Token{AccessToken: "fresh-access", Expiry: now.Add(4 * time.Hour)}
		return refreshed.WithExtra(map[string]interface{}{"id_token": newIDToken}), nil
	}

	requirement := dev6Requirement()
	requirement.TokenType = authcfg.TokenTypeIDToken
	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("near-expiry ID token must trigger refresh despite a distant access expiry (calls=%d)", refreshCalls)
	}
	if credential.Token != newIDToken {
		t.Fatalf("credential must carry the refreshed id token")
	}
	if credential.TokenType != authcfg.TokenTypeIDToken {
		t.Fatalf("credential type = %q", credential.TokenType)
	}
	// Credential expiry is the validated ID-token exp, never the access expiry.
	if credential.ExpiresAt.Unix() != newIDExpiry.Unix() {
		t.Fatalf("credential expiry = %v, want id-token exp %v", credential.ExpiresAt, newIDExpiry)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted.IDToken != newIDToken || persisted.IDTokenExpiresAt.Unix() != newIDExpiry.Unix() {
		t.Fatalf("refresh must store the new id token with its validated exp: %+v", persisted)
	}
	if persisted.IssuedAt.IsZero() {
		t.Fatalf("refresh must persist issued-at for the lifetime clamp")
	}
}

// TestResolveIDTokenRefreshOmissionRetainsValidToken: a refresh response
// without an id_token keeps the previous ID token while it is still valid.
func TestResolveIDTokenRefreshOmissionRetainsValidToken(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	oldIDExpiry := now.Add(30 * time.Minute)
	tok := seededDev6Token(now.Add(time.Hour))
	tok.TokenType = "idToken"
	tok.IDToken = idTokenValue(t, now.Add(-time.Hour), oldIDExpiry)
	tok.IDTokenExpiresAt = oldIDExpiry
	store.seed(tok)

	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "fresh-access", Expiry: now.Add(time.Hour)}, nil
	}
	requirement := dev6Requirement()
	requirement.TokenType = authcfg.TokenTypeIDToken

	credential, err := resolver.Refresh(delegatedCtx("uuid-1"), requirement)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if credential.Token != tok.IDToken {
		t.Fatalf("still-valid previous id token must be retained")
	}
	if credential.ExpiresAt.Unix() != oldIDExpiry.Unix() {
		t.Fatalf("credential expiry must stay the retained id token's exp, got %v", credential.ExpiresAt)
	}
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted.IDToken != tok.IDToken || persisted.IDTokenExpiresAt.Unix() != oldIDExpiry.Unix() {
		t.Fatalf("retained id token must keep its recorded exp: %+v", persisted)
	}
}

// TestResolveIDTokenExpiredWithoutReplacementIsLinkRequired: once the stored
// ID token is expired and refresh returns no replacement, the resolver returns
// typed link-required and never substitutes the access token.
func TestResolveIDTokenExpiredWithoutReplacementIsLinkRequired(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	tok := seededDev6Token(now.Add(time.Hour)) // access token still valid
	tok.TokenType = "idToken"
	tok.IDToken = idTokenValue(t, now.Add(-2*time.Hour), now.Add(-time.Minute))
	tok.IDTokenExpiresAt = now.Add(-time.Minute)
	store.seed(tok)

	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "fresh-access", Expiry: now.Add(time.Hour)}, nil
	}
	requirement := dev6Requirement()
	requirement.TokenType = authcfg.TokenTypeIDToken

	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if !authcfg.IsLinkRequired(err) {
		t.Fatalf("expired id token without replacement must be typed link-required, got cred=%+v err=%v", credential, err)
	}
	// The refreshed access token is persisted for compatible consumers, but the
	// expired ID token is dropped rather than served.
	persisted, _ := store.GetExact(context.Background(), "uuid-1", dev6StorageKey())
	if persisted == nil || persisted.AccessToken != "fresh-access" {
		t.Fatalf("access-token side of the grant must persist: %+v", persisted)
	}
	if persisted.IDToken != "" || !persisted.IDTokenExpiresAt.IsZero() {
		t.Fatalf("expired id token must be dropped from storage: %+v", persisted)
	}
}

// --- original-lifetime clamp (request-time and background) ---

// TestResolveLifetimeClampSkipsEarlyRefresh: a short-lived access token with
// known issued-at clamps the 15m lead to 20% of its lifetime, so a token five
// minutes from expiry with a ten-minute lifetime is NOT yet due.
func TestResolveLifetimeClampSkipsEarlyRefresh(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	tok := seededDev6Token(now.Add(5 * time.Minute))
	tok.IssuedAt = now.Add(-5 * time.Minute) // original lifetime: 10 minutes
	store.seed(tok)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	// newTestResolver's refreshToken fails the test if the endpoint is called.

	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if credential.Token != "dev6-access" {
		t.Fatalf("clamped token must be served without refresh, got %q", credential.Token)
	}

	// Without issued-at metadata the configured 15m lead applies and the same
	// token IS due (guarding against the historical always-zero lifetime).
	storeDue := newFakeDelegatedStore()
	storeDue.seed(seededDev6Token(now.Add(5 * time.Minute)))
	resolverDue := newTestResolver(t, storeDue, dev6ProviderDoc(false))
	var refreshCalls int
	resolverDue.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshCalls++
		return &oauth2.Token{AccessToken: "fresh", Expiry: now.Add(time.Hour)}, nil
	}
	if _, err := resolverDue.Resolve(delegatedCtx("uuid-1"), dev6Requirement()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("unknown lifetime must use the configured lead (calls=%d)", refreshCalls)
	}
}

// TestBackgroundRefreshAppliesLifetimeClamp: the watcher path makes the same
// clamped threshold decision as request-time resolution.
func TestBackgroundRefreshAppliesLifetimeClamp(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	tok := seededDev6Token(now.Add(5 * time.Minute))
	tok.IssuedAt = now.Add(-5 * time.Minute)
	store.seed(tok)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), tok); err != nil {
		t.Fatalf("clamped row must be skipped silently: %v", err)
	}
	// The endpoint stub fatals on call, so reaching here proves no refresh ran.
}

// TestBackgroundRefreshUsesIDTokenExpiry: rows stored for tokenType=idToken
// refresh on the ID-token exp even when the access token is far from expiry.
func TestBackgroundRefreshUsesIDTokenExpiry(t *testing.T) {
	store := newFakeDelegatedStore()
	now := time.Now()
	tok := seededDev6Token(now.Add(4 * time.Hour))
	tok.TokenType = "idToken"
	tok.IDToken = idTokenValue(t, now.Add(-55*time.Minute), now.Add(5*time.Minute))
	tok.IDTokenExpiresAt = now.Add(5 * time.Minute)
	store.seed(tok)
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	var calls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		calls++
		refreshed := &oauth2.Token{AccessToken: "bg-access", Expiry: now.Add(4 * time.Hour)}
		return refreshed.WithExtra(map[string]interface{}{"id_token": idTokenValue(t, now, now.Add(time.Hour))}), nil
	}
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), tok); err != nil {
		t.Fatalf("background refresh: %v", err)
	}
	if calls != 1 {
		t.Fatalf("id-token expiry must drive the background refresh decision (calls=%d)", calls)
	}
}

// --- inline providers ---

func inlineDev6Provider() *authcfg.OAuthProvider {
	return &authcfg.OAuthProvider{
		ID:            "inline-dev6",
		Issuer:        "https://idp-inline.example.com",
		DefaultClient: "inline-web",
		Clients: map[string]*authcfg.OAuthClient{
			"inline-web": {
				ConfigURL:   "scy://inline-client",
				RedirectURI: "https://steward.example.com/v1/api/auth/mcp/callback",
				UsePKCE:     true,
				RefreshLead: "15m",
			},
		},
	}
}

// TestInlineProviderEndToEnd: an inline provider resolves credentials, and —
// once registered in the runtime overlay — the background watcher routes its
// rows to the exact inline client/endpoint.
func TestInlineProviderEndToEnd(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store) // no registry-file providers
	inline := inlineDev6Provider()
	storageKey := DelegatedProviderStorageKey("test-ns", "inline-dev6")
	now := time.Now()
	store.seed(&OAuthToken{
		Username:     "uuid-1",
		Provider:     storageKey,
		AccessToken:  "inline-access",
		RefreshToken: "inline-refresh",
		ExpiresAt:    now.Add(5 * time.Minute),
		Issuer:       "https://idp-inline.example.com",
		Resource:     "https://mcp-inline.example.com/mcp",
		Scopes:       []string{"plan:read"},
		TokenType:    "accessToken",
		ProviderRef:  "inline-dev6",
		ClientRef:    "inline-web",
	})
	var refreshCalls int
	resolver.refreshToken = func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
		refreshCalls++
		return &oauth2.Token{AccessToken: "inline-fresh", Expiry: now.Add(time.Hour)}, nil
	}

	requirement := authcfg.Requirement{
		ServerName: "inline-mcp",
		Provider:   inline,
		Resource:   "https://mcp-inline.example.com/mcp",
		Scopes:     []string{"plan:read"},
		TokenType:  authcfg.TokenTypeAccessToken,
	}
	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if err != nil {
		t.Fatalf("inline resolve: %v", err)
	}
	if credential.Token != "inline-fresh" || refreshCalls != 1 {
		t.Fatalf("inline provider must resolve and refresh through its own client (token=%q calls=%d)", credential.Token, refreshCalls)
	}

	// Background refresh before overlay registration fails closed with the
	// actionable configuration requirement and never mutates the row.
	stored, _ := store.GetExact(context.Background(), "uuid-1", storageKey)
	stored.ExpiresAt = now.Add(time.Minute)
	stored.IssuedAt = now.Add(-59 * time.Minute) // original lifetime 1h: inside the lead
	casPuts := store.casPuts
	err = resolver.RefreshStoredDelegatedToken(context.Background(), stored)
	if err == nil || !strings.Contains(err.Error(), "inline overlay") {
		t.Fatalf("unregistered inline provider must fail closed with the configuration requirement, got %v", err)
	}
	if store.casPuts != casPuts {
		t.Fatalf("fail-closed routing must not mutate the row")
	}

	// After registration (MCP config load) the watcher routes correctly.
	if err := resolver.registry.RegisterInline(context.Background(), inline); err != nil {
		t.Fatalf("register inline: %v", err)
	}
	// Persist the due state: refresh acquires the lease and intentionally
	// reloads the protected row rather than trusting this caller's snapshot.
	store.seed(stored)
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), stored); err != nil {
		t.Fatalf("registered inline provider must refresh in background: %v", err)
	}
	if refreshCalls != 2 {
		t.Fatalf("background refresh must reach the inline client endpoint (calls=%d)", refreshCalls)
	}
}

// TestInlineProviderConflictsWithRegistryProvider: registry-file providers win
// for the same id; an issuer disagreement fails closed.
func TestInlineProviderConflictsWithRegistryProvider(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	conflicting := inlineDev6Provider()
	conflicting.ID = "adelphic-dev6" // same id as the registry file provider
	requirement := authcfg.Requirement{
		ServerName: "inline-mcp",
		Provider:   conflicting,
		TokenType:  authcfg.TokenTypeAccessToken,
	}
	_, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if err == nil || authcfg.IsLinkRequired(err) || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("issuer conflict with the registered provider must fail closed, got %v", err)
	}

	// Same id and same issuer: the registry-file definition wins silently.
	aligned := inlineDev6Provider()
	aligned.ID = "adelphic-dev6"
	aligned.Issuer = "https://idp-dev6.example.com/"
	store.seed(seededDev6Token(time.Now().Add(2 * time.Hour)))
	requirement.Provider = aligned
	credential, err := resolver.Resolve(delegatedCtx("uuid-1"), requirement)
	if err != nil {
		t.Fatalf("aligned inline definition must resolve via the file provider: %v", err)
	}
	if credential.Token != "dev6-access" {
		t.Fatalf("credential token = %q", credential.Token)
	}
}

// TestResolverFailsLoudlyWithoutEncryptionKey covers the fail-loud
// misconfiguration state installed when neither auth.tokenEncryptionKey nor a
// workspace OAuth configURL exists.
func TestResolverFailsLoudlyWithoutEncryptionKey(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.initErr = fmt.Errorf("delegated mcp oauth requires an encryption key for token storage")
	if _, err := resolver.Resolve(delegatedCtx("uuid-1"), dev6Requirement()); err == nil ||
		!strings.Contains(err.Error(), "encryption key") {
		t.Fatalf("missing encryption key must fail loudly, got %v", err)
	}
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), seededDev6Token(time.Now())); err == nil ||
		!strings.Contains(err.Error(), "encryption key") {
		t.Fatalf("background refresh must also fail loudly, got %v", err)
	}
}

func TestProviderRegistryMatchIssuerAmbiguity(t *testing.T) {
	first := dev6ProviderDoc(false)
	second := dev6ProviderDoc(false)
	second.ID = "adelphic-dev6-copy"
	loader := &fakeProviderLoader{docs: map[string]*oauthprovider.Document{
		first.ID:  first,
		second.ID: second,
	}}
	registry := providerregistry.NewWithLoader(loader)
	if _, err := registry.MatchIssuer(context.Background(), "https://idp-dev6.example.com"); err == nil {
		t.Fatalf("duplicate normalized issuers must hard-fail MatchIssuer")
	}
	if _, err := registry.ResolveProvider(context.Background(), "adelphic-dev6"); err != nil {
		t.Fatalf("explicit providerRef must still resolve: %v", err)
	}
}

func TestProviderRegistryIssuerNormalization(t *testing.T) {
	loader := &fakeProviderLoader{docs: map[string]*oauthprovider.Document{
		"adelphic-dev6": dev6ProviderDoc(false),
	}}
	registry := providerregistry.NewWithLoader(loader)
	provider, err := registry.MatchIssuer(context.Background(), "https://idp-dev6.example.com///")
	if err != nil {
		t.Fatalf("trailing slashes must normalize: %v", err)
	}
	if provider.ID != "adelphic-dev6" {
		t.Fatalf("matched wrong provider: %q", provider.ID)
	}
	if strings.Contains(authcfg.NormalizeIssuer(provider.Issuer), "//idp-dev6.example.com/") {
		t.Fatalf("issuer must be normalized for comparison")
	}
}
