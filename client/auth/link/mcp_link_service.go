package link

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/link/internal/authlog"
	"github.com/viant/mcp/client/auth/link/providerregistry"
	mcpcfg "github.com/viant/mcp/client/config"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
)

// Typed, non-enumerable failures surfaced by the MCP link endpoints. Unknown
// servers, unknown providers and non-delegated servers all collapse into
// errMCPLinkUnavailable so the endpoints cannot be used to enumerate
// configuration. Kill switches are intentionally distinguishable — the design
// requires a typed provider_disabled outcome plus an audit event.
var (
	errMCPLinkUnavailable  = fmt.Errorf("oauth_link_unavailable")
	errMCPProviderDisabled = fmt.Errorf("provider_disabled")
)

// MCPAuthConfigProvider yields MCP client configurations for the hosted link
// endpoints. The MCP manager's repository provider satisfies it; the runtime
// falls back to a workspace repository loader when none is installed.
type MCPAuthConfigProvider interface {
	Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error)
}

type mcpAuthConfigLister interface {
	Names(ctx context.Context) ([]string, error)
}

func (s *mcpLinkService) eagerStatuses(ctx context.Context, canonicalUserID, sessionID string) []*mcpStatusResult {
	lister, ok := s.configs.(mcpAuthConfigLister)
	if !ok || lister == nil {
		return nil
	}
	names, err := lister.Names(ctx)
	if err != nil {
		return nil
	}
	sort.Strings(names)
	result := make([]*mcpStatusResult, 0, len(names))
	for _, name := range names {
		cfg, loadErr := s.configs.Options(ctx, name)
		if loadErr != nil || cfg == nil || !cfg.EagerLink || !cfg.IsDelegatedAuth() || cfg.DisableDelegatedAuth {
			continue
		}
		status := s.status(ctx, name, canonicalUserID, sessionID)
		if status != nil {
			result = append(result, status)
		}
	}
	return result
}

// mcpLinkRateLimits bundles the bounded per-dimension endpoint limits.
type mcpLinkRateLimits struct {
	initiateUser *fixedWindowLimiter
	initiateIP   *fixedWindowLimiter
	callbackIP   *fixedWindowLimiter
	statusUser   *fixedWindowLimiter
}

func newMCPLinkRateLimits() *mcpLinkRateLimits {
	return &mcpLinkRateLimits{
		initiateUser: newFixedWindowLimiter(10, time.Minute),
		initiateIP:   newFixedWindowLimiter(30, time.Minute),
		callbackIP:   newFixedWindowLimiter(20, time.Minute),
		statusUser:   newFixedWindowLimiter(60, time.Minute),
	}
}

// pendingStateReader is the optional status-polling surface of a state store.
type pendingStateReader interface {
	GetPending(ctx context.Context, flowHash string) (*OAuthStateRecord, error)
}

// mcpLinkService implements the delegated MCP OAuth link flows behind the
// hosted /v1/api/auth/mcp endpoints. All persistence goes through the
// OAuthStateStore and DelegatedTokenStore adapters — no raw SQL here.
type mcpLinkService struct {
	cfg       *Config
	keyring   *mcpStateKeyring
	states    OAuthStateStore
	delegated *DelegatedMCPAuth
	configs   MCPAuthConfigProvider
	users     UserService
	limits    *mcpLinkRateLimits
	now       func() time.Time

	// Overridable collaborators (tests).
	loadClientConfig func(ctx context.Context, configURL string) (*oauth2.Config, error)
	exchangeCode     func(ctx context.Context, oauthCfg *oauth2.Config, code, redirectURI, codeVerifier, resource string) (*oauth2.Token, error)
	verifyJWT        mcpJWTVerifierFunc
	httpClient       *http.Client

	flowMu sync.Mutex
	flows  map[string]*sync.Mutex
}

func newMCPLinkService(cfg *Config, delegated *DelegatedMCPAuth, states OAuthStateStore, configs MCPAuthConfigProvider, users UserService) *mcpLinkService {
	if cfg == nil || delegated == nil || states == nil {
		return nil
	}
	keyring := newMCPStateKeyring(cfg)
	if keyring == nil {
		// Fail loud downstream: without key material the endpoints answer with
		// the generic unavailable error instead of sealing guessable state.
		return nil
	}
	if configs == nil {
		return nil
	}
	service := &mcpLinkService{
		cfg:              cfg,
		keyring:          keyring,
		states:           states,
		delegated:        delegated,
		configs:          configs,
		users:            users,
		limits:           newMCPLinkRateLimits(),
		now:              time.Now,
		loadClientConfig: loadOAuthClientConfig,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		flows:            map[string]*sync.Mutex{},
	}
	service.exchangeCode = func(ctx context.Context, oauthCfg *oauth2.Config, code, redirectURI, codeVerifier, resource string) (*oauth2.Token, error) {
		opts := []oauth2.AuthCodeOption{
			oauth2.SetAuthURLParam("redirect_uri", redirectURI),
			oauth2.SetAuthURLParam("code_verifier", codeVerifier),
		}
		if resource = strings.TrimSpace(resource); resource != "" {
			opts = append(opts, oauth2.SetAuthURLParam("resource", resource))
		}
		return oauthCfg.Exchange(ctx, code, opts...)
	}
	service.verifyJWT = service.defaultVerifyJWT()
	return service
}

// resolvedMCPLink is the fully resolved linking target for one MCP server.
type resolvedMCPLink struct {
	serverName  string
	requirement *authcfg.Requirement
	resolved    *resolvedProvider
}

// resolveServer resolves an MCP server name to its compiled delegated auth
// requirement and provider/client selection, enforcing the global, MCP-level
// and provider-level kill switches. Unknown/unconfigured servers return the
// non-enumerable errMCPLinkUnavailable.
func (s *mcpLinkService) resolveServer(ctx context.Context, serverName string) (*resolvedMCPLink, error) {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" || s.configs == nil {
		return nil, errMCPLinkUnavailable
	}
	if providerregistry.GloballyDisabled() {
		s.auditLink(ctx, "", serverName, "", "mcp_auth_kill_switch_activated", "global")
		return nil, errMCPProviderDisabled
	}
	cfg, err := s.configs.Options(ctx, serverName)
	if err != nil || cfg == nil || cfg.ClientOptions == nil || !cfg.IsDelegatedAuth() {
		return nil, errMCPLinkUnavailable
	}
	if cfg.DisableDelegatedAuth {
		s.auditLink(ctx, "", serverName, "", "mcp_auth_kill_switch_activated", "mcp")
		return nil, errMCPProviderDisabled
	}
	if err := cfg.NormalizeDelegatedAuth(); err != nil {
		return nil, errMCPLinkUnavailable
	}
	requirement, err := cfg.ClientOptions.Auth.CompileRequirement(ctx, serverName, strings.TrimSpace(cfg.ClientOptions.Transport.URL))
	if err != nil || requirement == nil {
		return nil, errMCPLinkUnavailable
	}
	resolver := s.delegated.resolver
	if resolver == nil {
		return nil, errMCPLinkUnavailable
	}
	if resolver.initErr != nil {
		// Misconfiguration (e.g. missing encryption key) must fail loudly for
		// operators; the HTTP layer still renders a non-enumerable error.
		return nil, resolver.initErr
	}
	resolved, err := resolver.resolveProvider(ctx, requirement)
	if err != nil {
		if providerregistry.IsDisabled(err) {
			s.auditLink(ctx, "", serverName, requirement.ProviderRef, "mcp_auth_kill_switch_activated", "provider")
			return nil, errMCPProviderDisabled
		}
		return nil, errMCPLinkUnavailable
	}
	// Inline providers become resolvable by the refresh watcher only through
	// the runtime overlay; register on every link interaction so a linked
	// credential can refresh after restart once its MCP config is touched.
	if requirement.Provider != nil {
		_ = s.delegated.registry.RegisterInline(ctx, requirement.Provider)
	}
	return &resolvedMCPLink{serverName: serverName, requirement: requirement, resolved: resolved}, nil
}

// userActive fails closed on disabled or deleted canonical users when a by-ID
// lookup is available; lookup errors reject linking rather than proceeding.
func (s *mcpLinkService) userActive(ctx context.Context, canonicalUserID string) bool {
	lookup, ok := s.users.(UserByIDLookup)
	if !ok || lookup == nil {
		return true
	}
	user, err := lookup.GetByID(ctx, canonicalUserID)
	if err != nil || user == nil {
		return false
	}
	return !user.Disabled
}

func (s *mcpLinkService) flowLock(flowHash string) *sync.Mutex {
	s.flowMu.Lock()
	defer s.flowMu.Unlock()
	lock, ok := s.flows[flowHash]
	if !ok {
		lock = &sync.Mutex{}
		s.flows[flowHash] = lock
	}
	return lock
}

// mcpInitiateResult is the JSON contract of POST initiate.
type mcpInitiateResult struct {
	Status            string `json:"status"`
	AuthorizationURL  string `json:"authorizationURL,omitempty"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

// initiate creates (or joins) the single authorization flow for one canonical
// user, provider, resource and scope set. Only the flow creator receives the
// authorization URL; concurrent callers across pods receive pending.
func (s *mcpLinkService) initiate(ctx context.Context, link *resolvedMCPLink, canonicalUserID, sessionID, returnURL, hostedCallback string, restart, forceRestart bool) (*mcpInitiateResult, error) {
	requirement := link.requirement
	resolved := link.resolved
	if !s.userActive(ctx, canonicalUserID) {
		return nil, errMCPLinkUnavailable
	}
	scopes := normalizeScopes(requirement.Scopes)
	flowHash := mcpFlowHash(canonicalUserID, resolved.refKey, requirement.Resource, scopes)
	lock := s.flowLock(flowHash)
	lock.Lock()
	defer lock.Unlock()

	redirectURI := strings.TrimSpace(resolved.client.RedirectURI)
	if redirectURI == "" {
		redirectURI = strings.TrimSpace(hostedCallback)
	}
	if redirectURI == "" {
		return nil, errMCPLinkUnavailable
	}
	returnURL = sanitizeMCPReturnURL(returnURL)
	nonce, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	fingerprint, _ := s.delegated.registry.Fingerprint(ctx)
	now := s.now().UTC()
	expiresAt := now.Add(mcpStateTTLForClient(s.cfg, resolved.client))
	codeVerifier := flow.GenerateCodeVerifier()
	sessionHash := s.keyring.mcpSessionHash(sessionID)
	statePayload := &MCPAuthState{
		CanonicalUserID:   canonicalUserID,
		SessionIDHash:     sessionHash,
		ServerName:        link.serverName,
		ProviderRef:       resolved.refKey,
		ClientRef:         resolved.clientName,
		Resource:          requirement.Resource,
		Scopes:            scopes,
		CodeVerifier:      codeVerifier,
		ReturnURL:         returnURL,
		Nonce:             nonce,
		ExpiresAt:         expiresAt,
		ConfigFingerprint: fingerprint,
		RedirectURI:       redirectURI,
	}
	stateBlob, err := s.keyring.encryptMCPAuthState(statePayload)
	if err != nil {
		return nil, err
	}
	stateHash := mcpStateHash(stateBlob)
	stateRecord := &OAuthStateRecord{
		StateHash:       stateHash,
		FlowHash:        flowHash,
		CanonicalUserID: canonicalUserID,
		SessionHash:     sessionHash,
		Provider:        resolved.refKey,
		ExpiresAt:       expiresAt,
	}
	stored, created, err := s.states.CreateOrGetPending(ctx, stateRecord)
	if err != nil {
		return nil, err
	}
	if !created && restart && stored != nil && (stored.SessionHash == sessionHash || forceRestart) {
		// An explicit reconnect may replace only a pending flow owned by this
		// exact browser/WebView session. A separately CSRF-protected explicit
		// forceRestart may also replace a stale flow for the same canonical user;
		// the flow hash is user-bound, and consuming the old state makes its
		// callback unusable before the new PKCE flow is returned.
		consumeSessionHash := sessionHash
		if forceRestart {
			consumeSessionHash = stored.SessionHash
		}
		if consumeErr := s.states.Consume(ctx, stored.StateHash, canonicalUserID, consumeSessionHash); consumeErr == nil {
			_, created, err = s.states.CreateOrGetPending(ctx, stateRecord)
			if err != nil {
				return nil, err
			}
		}
	}
	if !created {
		// A concurrent caller never reconstructs the owner's PKCE verifier or
		// authorization URL: it polls status until the owner completes or the
		// row expires.
		return &mcpInitiateResult{Status: "pending", RetryAfterSeconds: 2}, nil
	}
	authURL, err := s.buildAuthorizationURL(ctx, resolved, scopes, requirement.Resource, stateBlob, redirectURI, codeVerifier, nonce)
	if err != nil {
		// Free the flow row so a failed owner does not block relinking for the
		// whole TTL; the consume is best-effort.
		_ = s.states.Consume(ctx, stateHash, canonicalUserID, sessionHash)
		return nil, err
	}
	s.auditLink(ctx, canonicalUserID, link.serverName, resolved.refKey, "mcp_auth_link_initiated", "connect")
	return &mcpInitiateResult{Status: "connect", AuthorizationURL: authURL}, nil
}

func (s *mcpLinkService) buildAuthorizationURL(ctx context.Context, resolved *resolvedProvider, scopes []string, resource, stateBlob, redirectURI, codeVerifier, nonce string) (string, error) {
	if resolved.client == nil || strings.TrimSpace(resolved.client.ConfigURL) == "" {
		return "", fmt.Errorf("oauth provider %q client has no configURL", resolved.refKey)
	}
	oauthCfg, err := s.loadClientConfig(ctx, resolved.client.ConfigURL)
	if err != nil || oauthCfg == nil {
		if err == nil {
			err = fmt.Errorf("oauth client config unavailable for provider %q", resolved.refKey)
		}
		return "", err
	}
	options := []flow.Option{
		flow.WithPKCE(true),
		flow.WithState(stateBlob),
		flow.WithRedirectURI(redirectURI),
		flow.WithScopes(scopes...),
		flow.WithCodeVerifier(codeVerifier),
		flow.WithAuthURLParam("nonce", nonce),
	}
	if resource = strings.TrimSpace(resource); resource != "" {
		options = append(options, flow.WithAuthURLParam("resource", resource))
	}
	return flow.BuildAuthCodeURL(cloneOAuthConfigWithScopes(oauthCfg, scopes), options...)
}

// mcpStatusResult is the JSON contract of GET status. Unknown servers,
// unknown providers and unlinked users share the identical connected=false
// shape; provider/scopes/expiry appear only for a connected credential.
type mcpStatusResult struct {
	Server    string   `json:"server"`
	Provider  string   `json:"provider,omitempty"`
	Connected bool     `json:"connected"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
	Pending   bool     `json:"pending,omitempty"`
	CSRFToken string   `json:"csrfToken,omitempty"`
}

func (s *mcpLinkService) status(ctx context.Context, serverName, canonicalUserID, sessionID string) *mcpStatusResult {
	result := &mcpStatusResult{
		Server:    strings.TrimSpace(serverName),
		CSRFToken: s.keyring.mcpCSRFToken(sessionID),
	}
	link, err := s.resolveServer(ctx, serverName)
	if err != nil {
		// Non-enumerable: identical shape for unknown server, unknown provider,
		// disabled provider and unlinked user.
		return result
	}
	if !s.userActive(ctx, canonicalUserID) {
		return result
	}
	stored, err := s.delegated.resolver.store.GetExact(ctx, canonicalUserID, link.resolved.storageKey)
	if err == nil && stored != nil {
		now := s.now()
		tokenType := authcfg.TokenType(firstNonEmpty(stored.TokenType, string(link.requirement.TokenType), string(authcfg.TokenTypeAccessToken)))
		if selectedTokenValid(stored, tokenType, now) {
			result.Connected = true
			result.Provider = link.resolved.refKey
			// Legacy rows lacking delegated metadata stay connected with scopes
			// and expiry omitted — missing metadata is not an empty grant.
			if len(stored.Scopes) > 0 {
				result.Scopes = append([]string(nil), stored.Scopes...)
			}
			if expiry := selectedTokenExpiry(stored, tokenType); !expiry.IsZero() {
				result.ExpiresAt = expiry.UTC().Format(time.RFC3339)
			}
		}
	}
	if reader, ok := s.states.(pendingStateReader); ok && !result.Connected {
		flowHash := mcpFlowHash(canonicalUserID, link.resolved.refKey, link.requirement.Resource, link.requirement.Scopes)
		if pending, err := reader.GetPending(ctx, flowHash); err == nil && pending != nil && pending.CanonicalUserID == canonicalUserID {
			result.Pending = true
		}
	}
	return result
}

// disconnect deletes only the exact canonical-user/provider token, then
// notifies cache eviction. It is idempotent and non-enumerable: every path
// reports success.
func (s *mcpLinkService) disconnect(ctx context.Context, serverName, canonicalUserID, effectiveUserID string) {
	link, err := s.resolveServer(ctx, serverName)
	if err != nil {
		return
	}
	resolver := s.delegated.resolver
	stored, _ := resolver.store.GetExact(ctx, canonicalUserID, link.resolved.storageKey)
	if err := resolver.store.Delete(ctx, canonicalUserID, link.resolved.storageKey); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_disconnect",
			UserID:         canonicalUserID,
			Provider:       link.resolved.refKey,
			Classification: "persistence",
			Action:         "delete_failed_preserved",
		})
		return
	}
	if stored != nil {
		s.revokeBestEffort(ctx, link.resolved, stored)
	}
	resolver.clearCooldown(canonicalUserID, link.resolved.storageKey)
	notifyMCPAuthChange(s.cfg, MCPAuthChangeEvent{
		Kind:            "disconnected",
		CanonicalUserID: canonicalUserID,
		EffectiveUserID: effectiveUserID,
		ServerName:      link.serverName,
		ProviderRef:     link.resolved.refKey,
		StorageKey:      link.resolved.storageKey,
	})
	s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_disconnect", "deleted_exact")
}

// cleanupExpiredStates is owned by the auth runtime's background maintenance
// loop; it records deleted-row count and oldest-expired age metrics.
func (s *mcpLinkService) cleanupExpiredStates(ctx context.Context) {
	if s == nil || s.states == nil {
		return
	}
	now := s.now().UTC()
	deleted, oldest, err := s.states.DeleteExpired(ctx, now)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_state_cleanup",
			Classification: "persistence",
			Action:         "preserve",
			Err:            err,
		})
		return
	}
	if deleted == 0 {
		return
	}
	action := fmt.Sprintf("deleted_%d", deleted)
	if !oldest.IsZero() {
		action += fmt.Sprintf("_oldest_age_%s", now.Sub(oldest).Truncate(time.Second))
	}
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_state_cleanup",
		Classification: "maintenance",
		Action:         action,
	})
}

func (s *mcpLinkService) auditLink(ctx context.Context, userID, serverName, providerRef, op, action string) {
	authlog.Log(ctx, authlog.Event{
		Op:             op,
		UserID:         strings.TrimSpace(userID),
		Provider:       strings.TrimSpace(firstNonEmpty(providerRef, serverName)),
		Classification: "delegated_auth",
		Action:         action,
	})
}

// sanitizeMCPReturnURL allows only same-origin relative paths as post-link
// return targets; anything else falls back to the workspace root. Absolute
// URLs never round-trip through the provider state.
func sanitizeMCPReturnURL(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "//") || strings.Contains(candidate, "\\") {
		return ""
	}
	if parsed, err := url.Parse(candidate); err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return ""
	}
	return candidate
}

// hostedMCPCallbackURL derives the hosted callback for this deployment from
// the incoming request when the provider client does not pin a redirect URI.
func hostedMCPCallbackURL(r *http.Request) string {
	return callbackURL(r, "/v1/api/auth/mcp/callback")
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
