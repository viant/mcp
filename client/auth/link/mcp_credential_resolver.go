package link

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/viant/mcp/client/auth/link/internal/authlog"
	token "github.com/viant/mcp/client/auth/link/internal/token"
	"github.com/viant/mcp/client/auth/link/providerregistry"
	"golang.org/x/oauth2"

	authcfg "github.com/viant/mcp/client/auth/config"
	iauth "github.com/viant/mcp/client/auth/link/internal/authcontext"
)

// DelegatedTokenStore is the exact, canonical-user-keyed persistence surface
// used for delegated MCP credentials. It deliberately excludes the legacy
// fallback Get: delegated storage keys require exact matches and delegated
// owner resolution uses CanonicalUserID directly, never
// resolveOAuthTokenOwnerID.
type DelegatedTokenStore interface {
	GetExact(ctx context.Context, userID, provider string) (*OAuthToken, error)
	Put(ctx context.Context, token *OAuthToken) error
	Delete(ctx context.Context, userID, provider string) error
	TryAcquireRefreshLease(ctx context.Context, userID, provider, owner string, ttl time.Duration) (version int64, acquired bool, err error)
	ReleaseRefreshLease(ctx context.Context, userID, provider, owner string) error
	CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (swapped bool, err error)
}

// DelegatedTokenLister is the optional listing surface used by the
// user-lifecycle cleanup hook to enumerate a canonical user's delegated rows.
type DelegatedTokenLister interface {
	ListDelegated(ctx context.Context, userID string) ([]*OAuthToken, error)
}

// DelegatedTokenRefresher lets the background watcher refresh delegated rows
// through their exact provider broker. Until installed, delegated rows are
// skipped without mutation.
type DelegatedTokenRefresher interface {
	// RefreshStoredDelegatedToken applies the row's provider-specific refresh
	// policy and, when due, refreshes through that provider's exact
	// client/token endpoint using the distributed lease and CAS write.
	RefreshStoredDelegatedToken(ctx context.Context, stored *OAuthToken) error
	// MaxRefreshLead returns the largest configured provider refresh lead used
	// as the watcher's broad scan horizon.
	MaxRefreshLead(ctx context.Context) time.Duration
}

// providerColumnValidator is implemented by SQL-backed stores that can verify
// the live provider column width for the fixed-length delegated storage key.
type providerColumnValidator interface {
	ValidateProviderColumnWidth(ctx context.Context, width int) error
}

const delegatedRefreshCooldown = 30 * time.Second
const delegatedLeaseTTL = 30 * time.Second

// DelegatedCredentialResolver implements the viant/mcp CredentialResolver for
// Agently: canonical-user-keyed encrypted storage, validated workspace-token
// reuse, provider-exact refresh with distributed leases and CAS, and safe
// invalidation. Resolution returns outbound credentials only — it never
// installs values into the Agently authentication context.
type DelegatedCredentialResolver struct {
	cfg       *Config
	store     DelegatedTokenStore
	registry  *providerregistry.Registry
	namespace string
	workerID  string
	now       func() time.Time
	// initErr marks a fail-loud misconfiguration (e.g. no token encryption
	// key): every resolution returns it instead of silently disabling
	// delegated auth.
	initErr error
	// users, when installed, gates every delegated resolution and background
	// refresh on the canonical user's active status: disabled or deleted
	// users cannot use stored delegated credentials.
	users UserByIDLookup
	// loadClientConfig loads the SCY-referenced OAuth client (id, secret,
	// endpoints); overridable for tests.
	loadClientConfig func(ctx context.Context, configURL string) (*oauth2.Config, error)
	// refreshToken performs the token-endpoint refresh; overridable for tests.
	refreshToken func(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error)

	mu             sync.Mutex
	retryAt        map[string]time.Time
	inflight       map[string]*sync.Mutex
	columnChecked  bool
	columnCheckErr error
}

// NewDelegatedCredentialResolver builds the resolver. namespace is the
// immutable workspace namespace used in storage-key derivation.
func NewDelegatedCredentialResolver(cfg *Config, store DelegatedTokenStore, registry *providerregistry.Registry, namespace string) *DelegatedCredentialResolver {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	}
	return &DelegatedCredentialResolver{
		cfg:              cfg,
		store:            store,
		registry:         registry,
		namespace:        namespace,
		workerID:         runtimeWorkerID,
		now:              time.Now,
		loadClientConfig: loadOAuthClientConfig,
		refreshToken:     refreshOAuthToken,
		retryAt:          map[string]time.Time{},
		inflight:         map[string]*sync.Mutex{},
	}
}

// resolvedProvider carries a fully resolved provider/client selection.
type resolvedProvider struct {
	refKey     string
	provider   *authcfg.OAuthProvider
	client     *authcfg.OAuthClient
	clientName string
	storageKey string
}

func (r *DelegatedCredentialResolver) resolveProvider(ctx context.Context, requirement *authcfg.Requirement) (*resolvedProvider, error) {
	if providerregistry.GloballyDisabled() {
		r.auditKillSwitch(ctx, requirement, "global")
		return nil, &providerregistry.DisabledError{Ref: requirement.ProviderRef}
	}
	var (
		provider *authcfg.OAuthProvider
		refKey   string
		err      error
	)
	switch {
	case requirement.Provider != nil:
		provider = requirement.Provider
		refKey = strings.TrimSpace(provider.ID)
		if refKey == "" {
			return nil, fmt.Errorf("inline oauth provider requires a stable id for credential storage")
		}
		// Registry-file providers win over an inline definition with the same
		// id; a definition disagreeing on the issuer fails closed rather than
		// silently switching providers.
		if r.registry != nil {
			fileProvider, fileErr := r.registry.ResolveProvider(ctx, refKey)
			switch {
			case fileErr == nil && fileProvider != nil:
				if authcfg.NormalizeIssuer(fileProvider.Issuer) != authcfg.NormalizeIssuer(provider.Issuer) {
					return nil, fmt.Errorf("inline oauth provider %q conflicts with the registered provider of the same id (issuer %q vs %q)", refKey, provider.Issuer, fileProvider.Issuer)
				}
				provider = fileProvider
			case providerregistry.IsNotFound(fileErr):
				// No registered definition: the validated inline one applies.
			default:
				// Disabled providers and registry load failures fail closed.
				return nil, fileErr
			}
		}
	case strings.TrimSpace(requirement.ProviderRef) != "":
		refKey = strings.TrimSpace(requirement.ProviderRef)
		provider, err = r.registry.ResolveProvider(ctx, refKey)
		if err != nil {
			return nil, err
		}
	case strings.TrimSpace(requirement.Issuer) != "":
		// Challenge-mode learned issuer: only approved registry providers
		// match, and ambiguity fails closed.
		provider, err = r.registry.MatchIssuer(ctx, requirement.Issuer)
		if err != nil {
			return nil, err
		}
		refKey = strings.TrimSpace(provider.ID)
	default:
		return nil, fmt.Errorf("mcp %q: delegated auth requires providerRef, inlineProvider or a learned issuer", requirement.ServerName)
	}
	client, clientName, err := provider.Client(strings.TrimSpace(requirement.ClientRef))
	if err != nil {
		return nil, err
	}
	if requirement.Issuer != "" && provider.Issuer != "" &&
		authcfg.NormalizeIssuer(requirement.Issuer) != authcfg.NormalizeIssuer(provider.Issuer) {
		return nil, fmt.Errorf("mcp %q: required issuer %q does not match provider %q issuer %q",
			requirement.ServerName, requirement.Issuer, refKey, provider.Issuer)
	}
	if requirement.Issuer == "" {
		// Link routes compile workspace references before a registry is
		// attached to ClientAuth. Bind the issuer from the resolved provider.
		requirement.Issuer = authcfg.NormalizeIssuer(provider.Issuer)
	}
	return &resolvedProvider{
		refKey:     refKey,
		provider:   provider,
		client:     client,
		clientName: clientName,
		storageKey: DelegatedProviderStorageKey(r.namespace, refKey),
	}, nil
}

// canonicalUser fails closed when the request carries no canonical owner:
// delegated credentials are keyed by canonical users.id, and a missing value
// must never create or upsert a user or fall back to EffectiveUserID.
func (r *DelegatedCredentialResolver) canonicalUser(ctx context.Context) (string, error) {
	if strings.TrimSpace(iauth.EffectiveUserID(ctx)) == "" {
		return "", fmt.Errorf("delegated mcp auth requires an authenticated workspace user")
	}
	canonical := iauth.CanonicalUserID(ctx)
	if canonical == "" {
		return "", fmt.Errorf("delegated mcp auth requires a canonical workspace user; refusing to proceed without one")
	}
	if err := r.requireActiveUser(ctx, canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

// requireActiveUser revalidates the canonical user's active status when a
// by-ID lookup is installed. Disabled and deleted users fail closed; lookup
// failures also fail closed rather than serving a credential for a user whose
// status cannot be confirmed.
func (r *DelegatedCredentialResolver) requireActiveUser(ctx context.Context, canonical string) error {
	if r == nil || r.users == nil {
		return nil
	}
	user, err := r.users.GetByID(ctx, canonical)
	if err != nil {
		return fmt.Errorf("delegated mcp auth could not verify the canonical user status: %w", err)
	}
	if user == nil {
		return fmt.Errorf("delegated mcp auth refused: canonical user no longer exists")
	}
	if user.Disabled {
		return fmt.Errorf("delegated mcp auth refused: canonical user is disabled")
	}
	return nil
}

func (r *DelegatedCredentialResolver) ensureColumnWidth(ctx context.Context) error {
	validator, ok := r.store.(providerColumnValidator)
	if !ok {
		return nil
	}
	r.mu.Lock()
	if r.columnChecked {
		err := r.columnCheckErr
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()
	err := validator.ValidateProviderColumnWidth(ctx, DelegatedProviderKeyLength)
	r.mu.Lock()
	// Cache success only: a transient lookup failure fails this call closed
	// but is re-checked next time rather than poisoning the resolver.
	if err == nil {
		r.columnChecked = true
		r.columnCheckErr = nil
	}
	r.mu.Unlock()
	return err
}

// Resolve implements config.CredentialResolver.
func (r *DelegatedCredentialResolver) Resolve(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return r.resolve(ctx, &requirement, false)
}

// Refresh implements config.CredentialResolver. Per contract it bypasses any
// cached access token and mints a fresh credential while retaining the stored
// refresh credential.
func (r *DelegatedCredentialResolver) Refresh(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return r.resolve(ctx, &requirement, true)
}

func (r *DelegatedCredentialResolver) resolve(ctx context.Context, requirement *authcfg.Requirement, forceRefresh bool) (*authcfg.Credential, error) {
	if r != nil && r.initErr != nil {
		return nil, r.initErr
	}
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("delegated credential resolver is not configured")
	}
	canonical, err := r.canonicalUser(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := r.resolveProvider(ctx, requirement)
	if err != nil {
		return nil, err
	}
	if err := r.ensureColumnWidth(ctx); err != nil {
		return nil, err
	}
	// Validated workspace-token reuse: only on explicit ifCompatible policy
	// and only after full issuer/audience/scope/type/expiry verification of a
	// parseable JWT. A refresh request must mint through the provider, so
	// reuse applies to Resolve only.
	if !forceRefresh && requirement.ReusePolicy == authcfg.ReusePolicyIfCompatible {
		if credential := r.workspaceCompatibleCredential(ctx, requirement); credential != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "mcp_auth_workspace_token_reused",
				UserID:         canonical,
				Provider:       resolved.refKey,
				Classification: "delegated_auth",
				Action:         "reuse_workspace",
			})
			return credential, nil
		}
	}

	stored, err := r.store.GetExact(ctx, canonical, resolved.storageKey)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("no stored credential for provider %q", resolved.refKey))
	}
	if err := r.validateStored(stored, requirement, resolved); err != nil {
		return nil, authcfg.NewLinkRequired(requirement, err)
	}

	// Expiry, validity and refresh thresholds all derive from the *selected*
	// token: for tokenType=idToken that is the verified ID-token exp, never
	// the access-token expiry.
	now := r.now()
	lead := r.clientRefreshLead(resolved.client)
	effectiveLead := token.EffectiveRefreshLead(lead, selectedTokenLifetime(stored, requirement.TokenType))
	key := token.Key{Subject: canonical, Provider: resolved.storageKey}
	jitter := token.RefreshJitter(key, effectiveLead)
	selectedExpiry := selectedTokenExpiry(stored, requirement.TokenType)
	due := forceRefresh || r.refreshDue(now, selectedExpiry, effectiveLead, jitter)
	if requirement.TokenType == authcfg.TokenTypeIDToken && selectedExpiry.IsZero() {
		// An ID token without a verifiable exp can never be served; attempt to
		// mint a replacement before failing with link-required.
		due = true
	}

	if due {
		if strings.TrimSpace(stored.RefreshToken) == "" {
			// No refresh capability: keep using the selected token while valid;
			// at the threshold return link-required without calling the token
			// endpoint repeatedly.
			if !forceRefresh && selectedTokenValid(stored, requirement.TokenType, now) {
				return r.credentialFromStored(stored, requirement, resolved), nil
			}
			return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("stored credential for provider %q has no refresh token", resolved.refKey))
		}
		refreshed, err := r.refreshDelegated(ctx, canonical, requirement, resolved, stored, forceRefresh)
		if err != nil {
			if authcfg.IsLinkRequired(err) {
				return nil, err
			}
			// Temporary failure: continue using the current selected token
			// until its actual expiration.
			if !forceRefresh && selectedTokenValid(stored, requirement.TokenType, now) {
				return r.credentialFromStored(stored, requirement, resolved), nil
			}
			// Once the selected token is unusable—or the MCP transport has
			// explicitly rejected it and requested a forced refresh—there is no
			// credential the caller can safely retry. Surface the stable typed
			// link-required outcome even when the token endpoint returned a
			// transient or malformed response, so interactive hosts can start a
			// fresh authorization flow instead of exposing a decoder error.
			if forceRefresh || !selectedTokenValid(stored, requirement.TokenType, now) {
				return nil, authcfg.NewLinkRequired(requirement, err)
			}
			return nil, err
		}
		stored = refreshed
		// Authoritative refresh scopes: a narrowed grant is persisted, and an
		// insufficient set requires relinking for this MCP.
		if !scopesCover(stored.Scopes, requirement.Scopes) {
			return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("refreshed scopes no longer cover the mcp requirement"))
		}
	}
	if !selectedTokenValid(stored, requirement.TokenType, now) {
		if requirement.TokenType == authcfg.TokenTypeIDToken {
			// The refresh response omitted a replacement and the retained ID
			// token is expired (or carries no verifiable exp): typed
			// link-required, never a silent access-token substitution.
			return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("stored id token for provider %q expired and refresh returned no replacement", resolved.refKey))
		}
		return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("stored credential for provider %q expired", resolved.refKey))
	}
	return r.credentialFromStored(stored, requirement, resolved), nil
}

// selectedTokenExpiry returns the expiry of the token the requirement
// selects: the verified ID-token exp (persisted metadata or the exp claim of
// the stored JWT) for tokenType=idToken, otherwise the access-token expiry.
// Zero means unknown.
func selectedTokenExpiry(stored *OAuthToken, tokenType authcfg.TokenType) time.Time {
	if stored == nil {
		return time.Time{}
	}
	if tokenType == authcfg.TokenTypeIDToken {
		if !stored.IDTokenExpiresAt.IsZero() {
			return stored.IDTokenExpiresAt
		}
		return oauthJWTExpiry(stored.IDToken)
	}
	return stored.ExpiresAt
}

// selectedTokenLifetime derives the original lifetime of the selected token
// for the refresh-policy 20% clamp: expiry minus persisted issued-at, falling
// back to the iat claim of the selected JWT. Zero when unavailable.
func selectedTokenLifetime(stored *OAuthToken, tokenType authcfg.TokenType) time.Duration {
	expiry := selectedTokenExpiry(stored, tokenType)
	if stored == nil || expiry.IsZero() {
		return 0
	}
	issued := stored.IssuedAt
	if issued.IsZero() {
		selected := stored.AccessToken
		if tokenType == authcfg.TokenTypeIDToken {
			selected = stored.IDToken
		}
		if iat, ok := claimUnixTime(parseJWTClaims(selected), "iat"); ok {
			issued = iat
		}
	}
	if issued.IsZero() || !expiry.After(issued) {
		return 0
	}
	return expiry.Sub(issued)
}

// selectedTokenValid reports whether the selected token is present and
// unexpired. An ID token without a verifiable expiry fails closed: it can
// never satisfy the "retain only while still valid" contract.
func selectedTokenValid(stored *OAuthToken, tokenType authcfg.TokenType, now time.Time) bool {
	if stored == nil {
		return false
	}
	expiry := selectedTokenExpiry(stored, tokenType)
	if tokenType == authcfg.TokenTypeIDToken {
		return strings.TrimSpace(stored.IDToken) != "" && !expiry.IsZero() && expiry.After(now)
	}
	return expiry.IsZero() || expiry.After(now)
}

// Invalidate implements config.CredentialResolver. It deletes only the exact
// canonical-user/provider row. Unknown providers or a missing canonical owner
// must never delete credentials.
func (r *DelegatedCredentialResolver) Invalidate(ctx context.Context, requirement authcfg.Requirement) error {
	if r != nil && r.initErr != nil {
		return r.initErr
	}
	if r == nil || r.store == nil {
		return nil
	}
	canonical, err := r.canonicalUser(ctx)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_invalidate",
			Provider:       requirement.ProviderRef,
			Classification: "delegated_auth",
			Action:         "skip_no_canonical_user",
		})
		return nil
	}
	resolved, err := r.resolveProvider(ctx, &requirement)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_invalidate",
			UserID:         canonical,
			Provider:       requirement.ProviderRef,
			Classification: "delegated_auth",
			Action:         "skip_unknown_provider",
		})
		return nil
	}
	r.clearCooldown(canonical, resolved.storageKey)
	if err := r.store.Delete(ctx, canonical, resolved.storageKey); err != nil {
		return err
	}
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_invalidate",
		UserID:         canonical,
		Provider:       resolved.refKey,
		Classification: "delegated_auth",
		Action:         "deleted_exact",
	})
	return nil
}

// validateStored cross-checks stored credential metadata against the compiled
// requirement using parsed exact comparisons. Empty legacy metadata fields are
// not validated.
func (r *DelegatedCredentialResolver) validateStored(stored *OAuthToken, requirement *authcfg.Requirement, resolved *resolvedProvider) error {
	if stored.Issuer != "" && requirement.Issuer != "" &&
		authcfg.NormalizeIssuer(stored.Issuer) != authcfg.NormalizeIssuer(requirement.Issuer) {
		return fmt.Errorf("stored credential issuer does not match required issuer")
	}
	if stored.Resource != "" && requirement.Resource != "" && !exactURLEqual(stored.Resource, requirement.Resource) {
		return fmt.Errorf("stored credential resource does not match required resource")
	}
	if len(stored.Scopes) > 0 && !scopesCover(stored.Scopes, requirement.Scopes) {
		return fmt.Errorf("stored credential scopes do not cover the mcp requirement")
	}
	switch requirement.TokenType {
	case authcfg.TokenTypeIDToken:
		if strings.TrimSpace(stored.IDToken) == "" {
			return fmt.Errorf("stored credential has no id token")
		}
	default:
		// Never fall back from accessToken to idToken.
		if strings.TrimSpace(stored.AccessToken) == "" {
			return fmt.Errorf("stored credential has no access token")
		}
	}
	return nil
}

func (r *DelegatedCredentialResolver) credentialFromStored(stored *OAuthToken, requirement *authcfg.Requirement, resolved *resolvedProvider) *authcfg.Credential {
	value := strings.TrimSpace(stored.AccessToken)
	tokenType := authcfg.TokenTypeAccessToken
	if requirement.TokenType == authcfg.TokenTypeIDToken {
		// The selected value is always the ID token here; an access token is
		// never substituted (callers verified selected-token validity first).
		value = strings.TrimSpace(stored.IDToken)
		tokenType = authcfg.TokenTypeIDToken
	}
	resource := stored.Resource
	if resource == "" {
		resource = requirement.Resource
	}
	return &authcfg.Credential{
		Token:       value,
		TokenType:   tokenType,
		ExpiresAt:   selectedTokenExpiry(stored, requirement.TokenType),
		ProviderRef: resolved.refKey,
		Resource:    resource,
		Scopes:      append([]string(nil), stored.Scopes...),
	}
}

func (r *DelegatedCredentialResolver) clientRefreshLead(client *authcfg.OAuthClient) time.Duration {
	if client == nil {
		return token.DefaultRefreshLead
	}
	lead, err := client.RefreshLeadDuration()
	if err != nil || lead <= 0 {
		return token.DefaultRefreshLead
	}
	return lead
}

// refreshDue applies the shared refresh threshold to the selected token's
// expiry using an already-clamped effective lead plus deterministic jitter.
func (r *DelegatedCredentialResolver) refreshDue(now, expiresAt time.Time, effectiveLead, jitter time.Duration) bool {
	if expiresAt.IsZero() {
		return false
	}
	threshold := expiresAt.Add(-effectiveLead).Add(jitter)
	return !now.Before(threshold)
}

func (r *DelegatedCredentialResolver) cooldownKey(userID, storageKey string) string {
	return userID + "|" + storageKey
}

func (r *DelegatedCredentialResolver) inCooldown(userID, storageKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.retryAt[r.cooldownKey(userID, storageKey)]
	if !ok {
		return false
	}
	if r.now().Before(until) {
		return true
	}
	delete(r.retryAt, r.cooldownKey(userID, storageKey))
	return false
}

func (r *DelegatedCredentialResolver) setCooldown(userID, storageKey string) {
	r.mu.Lock()
	r.retryAt[r.cooldownKey(userID, storageKey)] = r.now().Add(delegatedRefreshCooldown)
	r.mu.Unlock()
}

func (r *DelegatedCredentialResolver) clearCooldown(userID, storageKey string) {
	r.mu.Lock()
	delete(r.retryAt, r.cooldownKey(userID, storageKey))
	r.mu.Unlock()
}

func (r *DelegatedCredentialResolver) refreshMutex(key string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.inflight[key]
	if !ok {
		m = &sync.Mutex{}
		r.inflight[key] = m
	}
	return m
}

// refreshDelegated refreshes a stored delegated credential through the exact
// provider client/token endpoint, serialized per canonical user and provider
// and coordinated across pods with the distributed lease plus CAS write. All
// stored metadata survives the write; the returned refresh scope set is
// authoritative when present.
func (r *DelegatedCredentialResolver) refreshDelegated(ctx context.Context, canonical string, requirement *authcfg.Requirement, resolved *resolvedProvider, stored *OAuthToken, force bool) (*OAuthToken, error) {
	if !force && r.inCooldown(canonical, resolved.storageKey) {
		return nil, fmt.Errorf("delegated refresh for provider %q is cooling down", resolved.refKey)
	}
	lock := r.refreshMutex(r.cooldownKey(canonical, resolved.storageKey))
	lock.Lock()
	defer lock.Unlock()

	version, acquired, err := r.store.TryAcquireRefreshLease(ctx, canonical, resolved.storageKey, r.workerID, delegatedLeaseTTL)
	if err != nil {
		r.setCooldown(canonical, resolved.storageKey)
		return nil, err
	}
	if !acquired {
		// Another pod is refreshing: wait briefly and adopt its result.
		time.Sleep(500 * time.Millisecond)
		reloaded, err := r.store.GetExact(ctx, canonical, resolved.storageKey)
		if err == nil && reloaded != nil && selectedTokenValid(reloaded, requirement.TokenType, r.now()) {
			return reloaded, nil
		}
		return nil, fmt.Errorf("delegated refresh for provider %q is already in progress", resolved.refKey)
	}
	release := func() {
		if err := r.store.ReleaseRefreshLease(ctx, canonical, resolved.storageKey, r.workerID); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "mcp_auth_lease_release",
				UserID:         canonical,
				Provider:       resolved.refKey,
				Classification: "lease",
				Action:         "preserve",
			})
		}
	}

	// The token passed by the caller may have been loaded before it waited on
	// the in-process mutex or distributed lease. Refresh tokens can be
	// one-time credentials, so always adopt the row protected by the lease
	// before calling the provider. Otherwise a waiter can replay the token a
	// preceding refresher just consumed and then invalidate its fresh result.
	latest, err := r.store.GetExact(ctx, canonical, resolved.storageKey)
	if err != nil {
		release()
		r.setCooldown(canonical, resolved.storageKey)
		return nil, err
	}
	if latest == nil || strings.TrimSpace(latest.RefreshToken) == "" {
		release()
		r.clearCooldown(canonical, resolved.storageKey)
		return nil, authcfg.NewLinkRequired(requirement, fmt.Errorf("stored credential for provider %q has no refresh token", resolved.refKey))
	}
	stored = latest
	if !force {
		now := r.now()
		lead := r.clientRefreshLead(resolved.client)
		effectiveLead := token.EffectiveRefreshLead(lead, selectedTokenLifetime(stored, requirement.TokenType))
		key := token.Key{Subject: canonical, Provider: resolved.storageKey}
		jitter := token.RefreshJitter(key, effectiveLead)
		if !r.refreshDue(now, selectedTokenExpiry(stored, requirement.TokenType), effectiveLead, jitter) {
			release()
			r.clearCooldown(canonical, resolved.storageKey)
			return stored, nil
		}
	}

	oauthCfg, err := r.loadClientConfig(ctx, resolved.client.ConfigURL)
	if err != nil || oauthCfg == nil {
		release()
		r.setCooldown(canonical, resolved.storageKey)
		if err == nil {
			err = fmt.Errorf("oauth client config unavailable for provider %q", resolved.refKey)
		}
		return nil, err
	}
	scopes := normalizeScopes(stored.Scopes)
	responsePolicy, err := tokenResponsePolicy(ctx, r.registry, resolved, requirement, oauthCfg)
	if err != nil {
		release()
		r.setCooldown(canonical, resolved.storageKey)
		return nil, err
	}
	if len(scopes) == 0 {
		scopes = normalizeScopes(requirement.Scopes)
	}
	resource := strings.TrimSpace(stored.Resource)
	if resource == "" {
		resource = strings.TrimSpace(requirement.Resource)
	}
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_refresh_started",
		UserID:         canonical,
		Provider:       resolved.refKey,
		Endpoint:       oauthCfg.Endpoint.TokenURL,
		Classification: "delegated_auth",
		Action:         "refresh",
	})
	exchangeCtx := ctx
	if responsePolicy != nil {
		exchangeCtx = pinnedTokenExchangeContext(ctx)
	}
	refreshed, err := r.refreshToken(exchangeCtx, cloneOAuthConfigWithScopes(oauthCfg, scopes), &oauth2.Token{RefreshToken: strings.TrimSpace(stored.RefreshToken)}, scopes, resource)
	if err != nil {
		release()
		if isPermanentRefreshError(err) {
			// invalid_grant invalidates only this provider's credential.
			if delErr := r.store.Delete(ctx, canonical, resolved.storageKey); delErr != nil {
				authlog.Log(ctx, authlog.Event{
					Op:             "mcp_auth_refresh_failed",
					UserID:         canonical,
					Provider:       resolved.refKey,
					Classification: "invalid_grant",
					Action:         "delete_failed_preserved",
				})
			}
			r.clearCooldown(canonical, resolved.storageKey)
			return nil, authcfg.NewLinkRequired(requirement, err)
		}
		r.setCooldown(canonical, resolved.storageKey)
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_refresh_failed",
			UserID:         canonical,
			Provider:       resolved.refKey,
			Classification: "refresh_error",
			Action:         "preserve_cooldown",
		})
		return nil, err
	}
	var responseGrant *verifiedGrant
	if responsePolicy != nil {
		responseGrant, err = validateTokenResponse(responsePolicy, requirement, refreshed, stored, r.now())
		if err != nil {
			release()
			r.setCooldown(canonical, resolved.storageKey)
			return nil, err
		}
	}
	next := *stored
	next.Username = canonical
	next.Provider = resolved.storageKey
	next.AccessToken = strings.TrimSpace(refreshed.AccessToken)
	next.ExpiresAt = refreshed.Expiry
	next.IssuedAt = r.now()
	// Preserve the previous refresh token when the provider omits a rotation.
	if rotated := strings.TrimSpace(refreshed.RefreshToken); rotated != "" {
		next.RefreshToken = rotated
	}
	// A returned ID token is validated (parseable, unexpired exp) before it is
	// stored; when the response omits one, the previous ID token is retained
	// only while still valid — an expired one is dropped, never served.
	next.IDToken = refreshedOAuthIDToken(refreshed, stored.IDToken)
	next.IDTokenExpiresAt = oauthJWTExpiry(next.IDToken)
	if responseScopes, present := oauthResponseScopes(refreshed); present {
		next.Scopes = responseScopes
	}
	if responseGrant != nil {
		next.Scopes = responseGrant.scopes
		next.Subject = responseGrant.subject
		// This policy authenticates an access-token grant only.
		next.IDToken = ""
		next.IDTokenExpiresAt = time.Time{}
	}
	next.ProviderRef = resolved.refKey
	if next.ClientRef == "" {
		next.ClientRef = resolved.clientName
	}
	// A refresh that would drop stored delegated metadata is a blocking error;
	// MergeMetadataFrom re-attaches anything the copy above left empty.
	next.MergeMetadataFrom(stored)
	if responseGrant != nil {
		// Do not resurrect scopes explicitly removed by the token endpoint.
		next.Scopes = responseGrant.scopes
	}

	swapped, err := r.store.CASPut(ctx, &next, version, r.workerID)
	if err != nil {
		release()
		r.setCooldown(canonical, resolved.storageKey)
		return nil, err
	}
	if !swapped {
		reloaded, err := r.store.GetExact(ctx, canonical, resolved.storageKey)
		if err != nil {
			return nil, err
		}
		if reloaded == nil || !selectedTokenValid(reloaded, requirement.TokenType, r.now()) {
			return nil, fmt.Errorf("delegated refresh for provider %q lost the CAS race", resolved.refKey)
		}
		next = *reloaded
	}
	r.clearCooldown(canonical, resolved.storageKey)
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_refresh_succeeded",
		UserID:         canonical,
		Provider:       resolved.refKey,
		Classification: "delegated_auth",
		Action:         "refreshed",
	})
	return &next, nil
}

// workspaceCompatibleCredential returns a credential built from the verified
// workspace token only when every compatibility requirement holds: verified
// signature (established upstream by session/bearer verification), normalized
// issuer equality, exact audience/resource membership, required-scope subset,
// unexpired, and token type match. Opaque tokens are never compatible.
func (r *DelegatedCredentialResolver) workspaceCompatibleCredential(ctx context.Context, requirement *authcfg.Requirement) *authcfg.Credential {
	bundle := iauth.TokensFromContext(ctx)
	if bundle == nil {
		return nil
	}
	candidate := strings.TrimSpace(bundle.AccessToken)
	tokenType := authcfg.TokenTypeAccessToken
	if requirement.TokenType == authcfg.TokenTypeIDToken {
		candidate = strings.TrimSpace(bundle.IDToken)
		tokenType = authcfg.TokenTypeIDToken
	}
	if candidate == "" {
		return nil
	}
	claims := parseJWTClaims(candidate)
	if len(claims) == 0 {
		// Opaque token: compatible only with verified introspection data,
		// which is not available here — fail closed.
		return nil
	}
	requiredIssuer := authcfg.NormalizeIssuer(requirement.Issuer)
	if requiredIssuer == "" {
		return nil
	}
	if authcfg.NormalizeIssuer(claimString(claims, "iss")) != requiredIssuer {
		return nil
	}
	expiresAt, ok := claimUnixTime(claims, "exp")
	if !ok || !expiresAt.After(r.now()) {
		return nil
	}
	if requirement.Resource != "" && !audienceContains(tokenAudiences(candidate), requirement.Resource) {
		return nil
	}
	granted := tokenScopesFromStrings(candidate)
	if len(requirement.Scopes) > 0 && !scopesCover(granted, requirement.Scopes) {
		return nil
	}
	return &authcfg.Credential{
		Token:       candidate,
		TokenType:   tokenType,
		ExpiresAt:   expiresAt,
		ProviderRef: requirement.ProviderRef,
		Resource:    requirement.Resource,
		Scopes:      granted,
	}
}

func (r *DelegatedCredentialResolver) auditKillSwitch(ctx context.Context, requirement *authcfg.Requirement, scope string) {
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_kill_switch_activated",
		Provider:       requirement.ProviderRef,
		Classification: "kill_switch",
		Action:         scope,
	})
}

// MaxRefreshLead implements DelegatedTokenRefresher.
func (r *DelegatedCredentialResolver) MaxRefreshLead(ctx context.Context) time.Duration {
	if r == nil || r.registry == nil {
		return 0
	}
	return r.registry.MaxRefreshLead(ctx)
}

// RefreshStoredDelegatedToken implements DelegatedTokenRefresher for the
// background watcher. Unknown providers, malformed metadata and disabled
// providers are skipped without modifying stored credentials; provider
// failures are isolated to the row.
func (r *DelegatedCredentialResolver) RefreshStoredDelegatedToken(ctx context.Context, stored *OAuthToken) error {
	if r != nil && r.initErr != nil {
		return r.initErr
	}
	if r == nil || stored == nil {
		return nil
	}
	canonical := strings.TrimSpace(stored.Username)
	storageKey := strings.TrimSpace(stored.Provider)
	providerRef := strings.TrimSpace(stored.ProviderRef)
	if canonical == "" || !IsDelegatedProviderKey(storageKey) {
		return nil
	}
	// Revalidate the canonical workspace user before any token-endpoint call:
	// disabled or deleted users cannot use (or refresh) delegated credentials.
	// The row is preserved without mutation.
	if err := r.requireActiveUser(ctx, canonical); err != nil {
		return err
	}
	if providerRef == "" {
		return fmt.Errorf("delegated row lacks providerRef metadata; skipped without mutation")
	}
	// Parse-and-validate: the storage key must be exactly the derivation of
	// this workspace namespace and the row's providerRef.
	if DelegatedProviderStorageKey(r.namespace, providerRef) != storageKey {
		return fmt.Errorf("delegated row storage key does not match providerRef %q in namespace %q; skipped", providerRef, r.namespace)
	}
	if providerregistry.GloballyDisabled() {
		return &providerregistry.DisabledError{Ref: providerRef}
	}
	lookupCtx := providerregistry.WithInlineLookupOwner(ctx, canonical)
	provider, err := r.registry.ResolveProvider(lookupCtx, providerRef)
	if err != nil {
		if providerregistry.IsNotFound(err) {
			// Inline MCP providers register into the runtime overlay when their
			// MCP configuration is loaded; after a restart that has not yet
			// happened. Fail closed with the configuration requirement instead
			// of guessing endpoints — the row is never mutated.
			return fmt.Errorf("delegated provider %q is not registered in the workspace oauth/providers registry or the runtime inline overlay; load the MCP configuration that defines it inline or add oauth/providers/%s.yaml to enable background refresh: %w", providerRef, providerRef, err)
		}
		return err
	}
	client, clientName, err := provider.Client(strings.TrimSpace(stored.ClientRef))
	if err != nil {
		return err
	}
	if strings.TrimSpace(stored.RefreshToken) == "" {
		// Tokens without refresh tokens are never sent to a token endpoint.
		return nil
	}
	// The background watcher applies exactly the same selected-token policy as
	// request-time resolution: expiry, original-lifetime clamp and jitter all
	// derive from the token type this row was stored for.
	storedType := authcfg.TokenType(firstNonEmpty(stored.TokenType, string(authcfg.TokenTypeAccessToken)))
	now := r.now()
	lead := r.clientRefreshLead(client)
	effectiveLead := token.EffectiveRefreshLead(lead, selectedTokenLifetime(stored, storedType))
	key := token.Key{Subject: canonical, Provider: storageKey}
	jitter := token.RefreshJitter(key, effectiveLead)
	if !r.refreshDue(now, selectedTokenExpiry(stored, storedType), effectiveLead, jitter) {
		return nil
	}
	requirement := &authcfg.Requirement{
		ServerName:  "background-refresh",
		ProviderRef: providerRef,
		ClientRef:   firstNonEmpty(stored.ClientRef, clientName),
		Issuer:      authcfg.NormalizeIssuer(firstNonEmpty(stored.Issuer, provider.Issuer)),
		Resource:    stored.Resource,
		Scopes:      authcfg.NormalizeScopes(stored.Scopes),
		TokenType:   storedType,
	}
	resolved := &resolvedProvider{
		refKey:     providerRef,
		provider:   provider,
		client:     client,
		clientName: clientName,
		storageKey: storageKey,
	}
	_, err = r.refreshDelegated(ctx, canonical, requirement, resolved, stored, false)
	if authcfg.IsLinkRequired(err) {
		// Permanent failure already invalidated only this provider; the next
		// interactive consumer receives link-required.
		return nil
	}
	return err
}

// scopesCover reports whether granted covers every required scope. An empty
// requirement is always covered.
func scopesCover(granted, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, scope := range normalizeScopes(granted) {
		set[scope] = true
	}
	for _, scope := range normalizeScopes(required) {
		if !set[scope] {
			return false
		}
	}
	return true
}

// audienceContains applies exact member equality over parsed audiences.
func audienceContains(audiences []string, resource string) bool {
	for _, audience := range audiences {
		if exactURLEqual(audience, resource) {
			return true
		}
	}
	return false
}

// exactURLEqual compares URLs by parsed exact equality (scheme, host, path,
// query). Prefix and substring matching are forbidden; non-URL values compare
// by string equality.
func exactURLEqual(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil || leftURL.Scheme == "" || rightURL.Scheme == "" {
		return left == right
	}
	return leftURL.Scheme == rightURL.Scheme &&
		leftURL.Host == rightURL.Host &&
		trimTrailingSlashes(leftURL.Path) == trimTrailingSlashes(rightURL.Path) &&
		leftURL.RawQuery == rightURL.RawQuery
}

func trimTrailingSlashes(path string) string {
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	return path
}
