package link

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/link/internal/authlog"
	vcfg "github.com/viant/scy/auth/jwt/verifier"
	"golang.org/x/oauth2"
)

// Typed callback failures. State problems (absent, expired, replayed,
// cross-session, cross-user, undecryptable) all surface as
// errMCPStateInvalid; the resource conflict is intentionally typed because
// operators must configure a separate providerRef instead of overwriting.
var (
	errMCPStateInvalid      = fmt.Errorf("oauth_state_invalid")
	errMCPLinkFailed        = fmt.Errorf("oauth_link_failed")
	errMCPResourceConflict  = fmt.Errorf("provider_resource_conflict")
	errMCPUnsupportedOpaque = fmt.Errorf("opaque access token requires a trusted introspection endpoint; delegated linking fails closed without one")
)

// mcpAllowedJWTAlgs is the signature-algorithm allowlist for delegated MCP
// token validation: asymmetric algorithms only. "none" and every HMAC variant
// are rejected outright to preclude algorithm-confusion downgrades.
var mcpAllowedJWTAlgs = map[string]bool{
	"RS256": true, "RS384": true, "RS512": true,
	"PS256": true, "PS384": true, "PS512": true,
	"ES256": true, "ES384": true, "ES512": true,
	"EdDSA": true,
}

// mcpJWTVerifierFunc cryptographically verifies a JWT against the provider's
// trusted JWKS and returns its claims. Implementations must reject signatures
// outside the algorithm allowlist; claim-level checks stay with the caller.
type mcpJWTVerifierFunc func(ctx context.Context, resolved *resolvedProvider, token string) (map[string]interface{}, error)

// jwksVerifierCacheEntry caches an initialized JWKS verifier per certificate
// URL so callbacks do not refetch keys on every request.
type jwksVerifierCacheEntry struct {
	service  *vcfg.Service
	cachedAt time.Time
}

const jwksVerifierCacheTTL = 10 * time.Minute

var (
	jwksVerifierMu    sync.Mutex
	jwksVerifierCache = map[string]*jwksVerifierCacheEntry{}
)

// defaultVerifyJWT builds the production verifier: algorithm allowlist first,
// then signature verification through the provider's discovered JWKS.
func (s *mcpLinkService) defaultVerifyJWT() mcpJWTVerifierFunc {
	return func(ctx context.Context, resolved *resolvedProvider, token string) (map[string]interface{}, error) {
		if err := checkJWTAlgorithmAllowlist(token); err != nil {
			return nil, err
		}
		jwksURL, err := s.providerJWKSURL(ctx, resolved)
		if err != nil {
			return nil, err
		}
		verifier, err := cachedJWKSVerifier(ctx, jwksURL)
		if err != nil {
			return nil, err
		}
		if _, err := verifier.VerifyClaims(ctx, token); err != nil {
			return nil, fmt.Errorf("token signature verification failed: %w", err)
		}
		claims := parseJWTClaims(token)
		if len(claims) == 0 {
			return nil, fmt.Errorf("verified token carries no claims")
		}
		return claims, nil
	}
}

func (s *mcpLinkService) providerJWKSURL(ctx context.Context, resolved *resolvedProvider) (string, error) {
	if resolved == nil || resolved.provider == nil {
		return "", fmt.Errorf("oauth provider is not resolved")
	}
	if discovery := strings.TrimSpace(resolved.provider.DiscoveryURL); discovery != "" {
		return fetchOpenIDJWKSURL(ctx, discovery)
	}
	if issuer := strings.TrimSpace(resolved.provider.Issuer); issuer != "" {
		return fetchIssuerJWKSURL(ctx, issuer)
	}
	return "", fmt.Errorf("oauth provider %q has no discovery metadata for JWKS resolution", resolved.refKey)
}

func cachedJWKSVerifier(ctx context.Context, jwksURL string) (*vcfg.Service, error) {
	jwksURL = strings.TrimSpace(jwksURL)
	if jwksURL == "" {
		return nil, fmt.Errorf("jwks url is empty")
	}
	now := time.Now()
	jwksVerifierMu.Lock()
	if entry, ok := jwksVerifierCache[jwksURL]; ok && now.Sub(entry.cachedAt) < jwksVerifierCacheTTL {
		service := entry.service
		jwksVerifierMu.Unlock()
		return service, nil
	}
	jwksVerifierMu.Unlock()
	service := vcfg.New(&vcfg.Config{CertURL: jwksURL})
	if err := service.Init(ctx); err != nil {
		return nil, fmt.Errorf("jwks verifier init failed: %w", err)
	}
	jwksVerifierMu.Lock()
	jwksVerifierCache[jwksURL] = &jwksVerifierCacheEntry{service: service, cachedAt: now}
	jwksVerifierMu.Unlock()
	return service, nil
}

func checkJWTAlgorithmAllowlist(token string) error {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return fmt.Errorf("token is not a JWS compact serialization")
	}
	header, err := decodeJWTSegment(parts[0])
	if err != nil {
		return fmt.Errorf("malformed token header")
	}
	alg, _ := header["alg"].(string)
	if !mcpAllowedJWTAlgs[strings.TrimSpace(alg)] {
		return fmt.Errorf("token algorithm %q is not allowlisted", strings.TrimSpace(alg))
	}
	return nil
}

// verifiedGrant carries only values extracted after successful cryptographic
// validation of the granted tokens.
type verifiedGrant struct {
	subject          string
	issuer           string
	scopes           []string
	accessExpiresAt  time.Time
	idToken          string
	idTokenExpiresAt time.Time
}

// clockSkew returns the provider client's configured skew, defaulting to 30s.
func clockSkew(client *authcfg.OAuthClient) time.Duration {
	if client != nil {
		if skew, err := client.ClockSkewDuration(); err == nil && skew > 0 {
			return skew
		}
	}
	return 30 * time.Second
}

// validateGrant cryptographically validates the exchanged token set against
// the compiled requirement: signature via trusted JWKS with an algorithm
// allowlist, exact normalized issuer, exact audience/resource membership,
// exp/nbf/iat with configured skew, authoritative scope coverage and the
// provider subject taken only from verified material. Opaque access tokens
// require configured authenticated introspection and otherwise fail closed.
func (s *mcpLinkService) validateGrant(ctx context.Context, link *resolvedMCPLink, token *oauth2.Token, expectedNonce string) (*verifiedGrant, error) {
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("token exchange returned no access token")
	}
	requirement := link.requirement
	resolved := link.resolved
	expectedIssuer := authcfg.NormalizeIssuer(firstNonEmpty(requirement.Issuer, resolved.provider.Issuer))
	if expectedIssuer == "" {
		return nil, fmt.Errorf("provider %q has no configured issuer", resolved.refKey)
	}
	skew := clockSkew(resolved.client)
	now := s.now()
	grant := &verifiedGrant{issuer: expectedIssuer, accessExpiresAt: token.Expiry}

	rawIDToken, _ := token.Extra("id_token").(string)
	rawIDToken = strings.TrimSpace(rawIDToken)

	accessToken := strings.TrimSpace(token.AccessToken)
	var accessClaims map[string]interface{}
	if looksLikeJWT(accessToken) {
		claims, err := s.verifyJWT(ctx, resolved, accessToken)
		if err != nil {
			return nil, fmt.Errorf("access token rejected: %w", err)
		}
		if err := validateVerifiedClaims(claims, expectedIssuer, requirement.Resource, now, skew, true); err != nil {
			return nil, fmt.Errorf("access token rejected: %w", err)
		}
		accessClaims = claims
		if exp, ok := claimUnixTime(claims, "exp"); ok {
			grant.accessExpiresAt = exp
		}
	} else {
		claims, err := s.introspectOpaqueToken(ctx, link, accessToken)
		if err != nil {
			return nil, err
		}
		if err := validateVerifiedClaims(claims, expectedIssuer, requirement.Resource, now, skew, true); err != nil {
			return nil, fmt.Errorf("access token introspection rejected: %w", err)
		}
		accessClaims = claims
		if exp, ok := claimUnixTime(claims, "exp"); ok {
			grant.accessExpiresAt = exp
		}
	}

	// The granted scope set is authoritative: the token response scope
	// parameter when present, otherwise the verified token/introspection
	// scope claims.
	granted, present := oauthResponseScopes(token)
	if !present || len(granted) == 0 {
		granted = scopesFromClaims(accessClaims)
	}
	granted = normalizeScopes(granted)
	if !scopesCover(granted, requirement.Scopes) {
		return nil, fmt.Errorf("granted scopes do not cover the mcp requirement")
	}
	grant.scopes = granted

	needIDToken := requirement.TokenType == authcfg.TokenTypeIDToken
	if rawIDToken != "" {
		claims, err := s.verifyJWT(ctx, resolved, rawIDToken)
		if err == nil {
			err = validateIDTokenClaims(claims, expectedIssuer, expectedNonce, now, skew, s.audienceForIDToken(ctx, resolved))
		}
		if err != nil {
			if needIDToken {
				return nil, fmt.Errorf("id token rejected: %w", err)
			}
			// An unverifiable optional ID token is dropped, never stored.
			authlog.Log(ctx, authlog.Event{
				Op:             "mcp_auth_link_id_token_dropped",
				Provider:       resolved.refKey,
				Classification: "token_validation",
				Action:         "drop_unverified",
				Err:            err,
			})
			rawIDToken = ""
		} else {
			grant.idToken = rawIDToken
			if exp, ok := claimUnixTime(claims, "exp"); ok {
				grant.idTokenExpiresAt = exp
			}
			if grant.subject == "" {
				grant.subject = claimString(claims, "sub")
			}
		}
	}
	if needIDToken && grant.idToken == "" {
		return nil, fmt.Errorf("provider returned no id token for a tokenType=idToken requirement")
	}
	if subject := claimString(accessClaims, "sub"); subject != "" {
		// The provider subject prefers the token the requirement selects.
		if !needIDToken || grant.subject == "" {
			grant.subject = subject
		}
	}
	if strings.TrimSpace(grant.subject) == "" {
		return nil, fmt.Errorf("verified token carries no subject")
	}
	if grant.accessExpiresAt.IsZero() {
		return nil, fmt.Errorf("verified token carries no expiration")
	}
	return grant, nil
}

// audienceForIDToken returns the OAuth client ID the ID-token audience must
// contain (OIDC: aud includes the relying party client_id).
func (s *mcpLinkService) audienceForIDToken(ctx context.Context, resolved *resolvedProvider) string {
	if resolved == nil || resolved.client == nil {
		return ""
	}
	oauthCfg, err := s.loadClientConfig(ctx, resolved.client.ConfigURL)
	if err != nil || oauthCfg == nil {
		return ""
	}
	return strings.TrimSpace(oauthCfg.ClientID)
}

func looksLikeJWT(token string) bool {
	if strings.Count(token, ".") != 2 {
		return false
	}
	return len(parseJWTClaims(token)) > 0
}

// validateVerifiedClaims applies the exact issuer, exact audience/resource,
// and exp/nbf/iat clock-skew checks to already signature-verified (or
// introspection-confirmed) claims.
func validateVerifiedClaims(claims map[string]interface{}, expectedIssuer, resource string, now time.Time, skew time.Duration, requireExp bool) error {
	if authcfg.NormalizeIssuer(claimString(claims, "iss")) != expectedIssuer {
		return fmt.Errorf("issuer mismatch")
	}
	exp, hasExp := claimUnixTime(claims, "exp")
	if requireExp && !hasExp {
		return fmt.Errorf("missing expiration")
	}
	if hasExp && !exp.Add(skew).After(now) {
		return fmt.Errorf("token expired")
	}
	if nbf, ok := claimUnixTime(claims, "nbf"); ok && nbf.Add(-skew).After(now) {
		return fmt.Errorf("token not yet valid")
	}
	if iat, ok := claimUnixTime(claims, "iat"); ok && iat.Add(-skew).After(now) {
		return fmt.Errorf("token issued in the future")
	}
	if strings.TrimSpace(resource) != "" {
		audiences := claimAudiences(claims)
		if !audienceContains(audiences, resource) {
			return fmt.Errorf("audience %q does not include the required resource %q", audiences, resource)
		}
	}
	return nil
}

func validateIDTokenClaims(claims map[string]interface{}, expectedIssuer, expectedNonce string, now time.Time, skew time.Duration, clientID string) error {
	if authcfg.NormalizeIssuer(claimString(claims, "iss")) != expectedIssuer {
		return fmt.Errorf("issuer mismatch")
	}
	exp, ok := claimUnixTime(claims, "exp")
	if !ok {
		return fmt.Errorf("missing expiration")
	}
	if !exp.Add(skew).After(now) {
		return fmt.Errorf("id token expired")
	}
	if iat, ok := claimUnixTime(claims, "iat"); ok && iat.Add(-skew).After(now) {
		return fmt.Errorf("id token issued in the future")
	}
	if clientID != "" && !audienceContains(claimAudiences(claims), clientID) {
		return fmt.Errorf("id token audience does not include the client")
	}
	if expectedNonce != "" {
		if nonce := claimString(claims, "nonce"); nonce != "" && nonce != expectedNonce {
			return fmt.Errorf("id token nonce mismatch")
		}
	}
	return nil
}

func claimAudiences(claims map[string]interface{}) []string {
	switch actual := claims["aud"].(type) {
	case string:
		return []string{strings.TrimSpace(actual)}
	case []interface{}:
		var audiences []string
		for _, item := range actual {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				audiences = append(audiences, strings.TrimSpace(value))
			}
		}
		return audiences
	case []string:
		return actual
	default:
		return nil
	}
}

func scopesFromClaims(claims map[string]interface{}) []string {
	if claims == nil {
		return nil
	}
	var scopes []string
	switch actual := claims["scope"].(type) {
	case string:
		scopes = strings.Fields(actual)
	case []interface{}:
		for _, item := range actual {
			if value, ok := item.(string); ok {
				scopes = append(scopes, value)
			}
		}
	}
	if raw, ok := claims["scp"].([]interface{}); ok {
		for _, item := range raw {
			if value, ok := item.(string); ok {
				scopes = append(scopes, value)
			}
		}
	}
	return normalizeScopes(scopes)
}

// introspectOpaqueToken validates an opaque access token through the
// provider's configured RFC 7662 endpoint using the confidential client's SCY
// credentials. Providers without configured introspection (including inline
// providers) fail closed.
func (s *mcpLinkService) introspectOpaqueToken(ctx context.Context, link *resolvedMCPLink, accessToken string) (map[string]interface{}, error) {
	doc, err := s.delegated.registry.Provider(ctx, link.resolved.refKey)
	if err != nil || doc == nil || doc.Introspection == nil || strings.TrimSpace(doc.Introspection.URL) == "" {
		return nil, errMCPUnsupportedOpaque
	}
	endpoint := strings.TrimSpace(doc.Introspection.URL)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, errMCPUnsupportedOpaque
	}
	if parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("introspection endpoint must use https")
	}
	introClient := link.resolved.client
	if ref := strings.TrimSpace(doc.Introspection.ClientRef); ref != "" {
		selected, _, cErr := doc.OAuthProvider.Client(ref)
		if cErr != nil {
			return nil, fmt.Errorf("introspection client %q: %w", ref, cErr)
		}
		introClient = selected
	}
	if introClient == nil || strings.TrimSpace(introClient.ConfigURL) == "" {
		return nil, errMCPUnsupportedOpaque
	}
	oauthCfg, err := s.loadClientConfig(ctx, introClient.ConfigURL)
	if err != nil || oauthCfg == nil || strings.TrimSpace(oauthCfg.ClientID) == "" {
		return nil, fmt.Errorf("introspection client credentials unavailable")
	}
	form := url.Values{}
	form.Set("token", accessToken)
	form.Set("token_type_hint", "access_token")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(url.QueryEscape(oauthCfg.ClientID), url.QueryEscape(oauthCfg.ClientSecret))
	httpClient := s.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("introspection request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection returned status %d", response.StatusCode)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("introspection response malformed: %w", err)
	}
	if active, _ := payload["active"].(bool); !active {
		return nil, fmt.Errorf("introspection reports token inactive")
	}
	// The spec-mandated fields must be present and valid; the shared claim
	// validator enforces issuer/audience/expiry and the caller covers scopes
	// and subject.
	if strings.TrimSpace(claimString(payload, "sub")) == "" {
		return nil, fmt.Errorf("introspection response carries no subject")
	}
	if _, ok := claimUnixTime(payload, "exp"); !ok {
		return nil, fmt.Errorf("introspection response carries no expiration")
	}
	return payload, nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// mcpCallbackResult reports a completed link for response rendering.
type mcpCallbackResult struct {
	ServerName  string
	ProviderRef string
	ReturnURL   string
}

// completeCallback executes the full hosted-callback sequence: decrypt and
// authenticate state, bind it to the active workspace session and canonical
// user, atomically consume it BEFORE the code exchange, re-resolve and
// fingerprint-check configuration, exchange the code with PKCE and the
// confidential client, cryptographically validate the grant, reject
// provider/resource conflicts and persist the credential under the canonical
// user. It never touches the workspace session, cookies or auth context.
func (s *mcpLinkService) completeCallback(ctx context.Context, code, stateBlob string, sess *Session, canonicalUserID, effectiveUserID string) (*mcpCallbackResult, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(stateBlob) == "" {
		return nil, errMCPStateInvalid
	}
	payload, err := s.keyring.decryptMCPAuthState(stateBlob)
	if err != nil {
		return nil, errMCPStateInvalid
	}
	now := s.now()
	if !payload.ExpiresAt.After(now) {
		return nil, errMCPStateInvalid
	}
	// Session binding: the callback must arrive on the same active workspace
	// session that initiated the flow (previous-key hashes cover rotation).
	sessionBound := false
	for _, hash := range s.keyring.mcpSessionHashes(sess.ID) {
		if hash != "" && hash == payload.SessionIDHash {
			sessionBound = true
			break
		}
	}
	if !sessionBound {
		return nil, errMCPStateInvalid
	}
	if strings.TrimSpace(canonicalUserID) == "" || payload.CanonicalUserID != canonicalUserID {
		return nil, errMCPStateInvalid
	}
	if !s.userActive(ctx, canonicalUserID) {
		return nil, errMCPStateInvalid
	}
	// Atomic single-use consume happens before the token exchange: a replay
	// or a cross-pod duplicate fails here and never reaches the provider.
	if err := s.states.Consume(ctx, mcpStateHash(stateBlob), canonicalUserID, payload.SessionIDHash); err != nil {
		return nil, errMCPStateInvalid
	}
	link, err := s.resolveServer(ctx, payload.ServerName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.ProviderRef) != link.resolved.refKey {
		s.auditLink(ctx, canonicalUserID, payload.ServerName, payload.ProviderRef, "mcp_auth_link_failed", "provider_changed")
		return nil, errMCPLinkFailed
	}
	// Configuration fingerprint check: a provider-registry change between
	// initiation and callback invalidates the flow instead of exchanging under
	// different configuration.
	if fingerprint, fErr := s.delegated.registry.Fingerprint(ctx); fErr == nil && payload.ConfigFingerprint != "" && fingerprint != payload.ConfigFingerprint {
		s.auditLink(ctx, canonicalUserID, payload.ServerName, link.resolved.refKey, "mcp_auth_link_failed", "config_fingerprint_changed")
		return nil, errMCPLinkFailed
	}
	oauthCfg, err := s.loadClientConfig(ctx, link.resolved.client.ConfigURL)
	if err != nil || oauthCfg == nil {
		s.auditLink(ctx, canonicalUserID, payload.ServerName, link.resolved.refKey, "mcp_auth_link_failed", "client_config_unavailable")
		return nil, errMCPLinkFailed
	}
	scopes := normalizeScopes(payload.Scopes)
	responsePolicy, err := tokenResponsePolicy(ctx, s.delegated.registry, link.resolved, link.requirement, oauthCfg)
	if err != nil {
		authlog.Log(ctx, authlog.Event{Op: "mcp_auth_link_failed", Provider: link.resolved.refKey, Classification: "token_response_policy", Action: "reject", Err: err})
		return nil, errMCPLinkFailed
	}
	if responsePolicy != nil && !exactURLEqual(payload.Resource, responsePolicy.Resource) {
		return nil, errMCPLinkFailed
	}
	exchangeCtx := ctx
	if responsePolicy != nil {
		exchangeCtx = pinnedTokenExchangeContext(ctx)
	}
	token, err := s.exchangeCode(exchangeCtx, cloneOAuthConfigWithScopes(oauthCfg, scopes), code, payload.RedirectURI, payload.CodeVerifier, payload.Resource)
	if err != nil {
		// The state is already consumed: the user starts a new authorization
		// flow, which is exactly the mandated recovery.
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_exchange_after_state_consume_failed",
			UserID:         canonicalUserID,
			Provider:       link.resolved.refKey,
			Classification: "delegated_auth",
			Action:         "exchange_failed",
			Err:            err,
		})
		return nil, errMCPLinkFailed
	}
	var grant *verifiedGrant
	if responsePolicy != nil {
		grant, err = validateTokenResponse(responsePolicy, link.requirement, token, nil, s.now())
	} else {
		grant, err = s.validateGrant(ctx, link, token, payload.Nonce)
	}
	if err != nil {
		s.auditLink(ctx, canonicalUserID, payload.ServerName, link.resolved.refKey, "mcp_auth_link_failed", "token_validation_failed")
		authlog.Log(ctx, authlog.Event{Op: "mcp_auth_link_failed", Provider: link.resolved.refKey, Classification: "grant_validation", Action: "reject", Err: err})
		return nil, errMCPLinkFailed
	}
	if err := s.persistVerifiedGrant(ctx, link, token, grant, canonicalUserID, effectiveUserID); err != nil {
		return nil, err
	}
	return &mcpCallbackResult{
		ServerName:  link.serverName,
		ProviderRef: link.resolved.refKey,
		ReturnURL:   sanitizeMCPReturnURL(payload.ReturnURL),
	}, nil
}

// completeOOBLink validates and stores a provider grant obtained by a local
// out-of-band OAuth client. The active workspace session is enforced by the
// HTTP handler; this method independently re-resolves server policy, checks
// canonical-user activity and applies the same validation/persistence rules
// as the authorization-code callback.
func (s *mcpLinkService) completeOOBLink(ctx context.Context, serverName string, token *oauth2.Token, canonicalUserID, effectiveUserID string) (*mcpCallbackResult, error) {
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	if canonicalUserID == "" || !s.userActive(ctx, canonicalUserID) {
		return nil, errMCPLinkFailed
	}
	link, err := s.resolveServer(ctx, serverName)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_oob_resolution",
			UserID:         canonicalUserID,
			Classification: "provider_resolution",
			Action:         "reject",
			Err:            err,
		})
		return nil, errMCPLinkFailed
	}
	grant, err := s.validateGrant(ctx, link, token, "")
	if err != nil {
		s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_oob_failed", "token_validation_failed")
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_oob_validation",
			UserID:         canonicalUserID,
			Provider:       link.resolved.refKey,
			Classification: "token_validation",
			Action:         "reject",
			Err:            err,
		})
		return nil, errMCPLinkFailed
	}
	if err := s.persistVerifiedGrant(ctx, link, token, grant, canonicalUserID, effectiveUserID); err != nil {
		return nil, err
	}
	s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_oob_succeeded", "linked")
	return &mcpCallbackResult{ServerName: link.serverName, ProviderRef: link.resolved.refKey}, nil
}

// persistVerifiedGrant is shared by browser-code and OOB linking. Callers
// must pass a grant returned by validateGrant; raw client token envelopes are
// never persisted directly.
func (s *mcpLinkService) persistVerifiedGrant(ctx context.Context, link *resolvedMCPLink, token *oauth2.Token, grant *verifiedGrant, canonicalUserID, effectiveUserID string) error {
	now := s.now()
	resolver := s.delegated.resolver
	storageKey := link.resolved.storageKey
	stored, err := resolver.store.GetExact(ctx, canonicalUserID, storageKey)
	if err != nil {
		return errMCPLinkFailed
	}
	// Version one permits one resource token per storage key: an existing row
	// granted by another issuer or for another resource is never overwritten.
	if stored != nil {
		if stored.Issuer != "" && authcfg.NormalizeIssuer(stored.Issuer) != grant.issuer {
			s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_link_failed", "issuer_conflict")
			return errMCPResourceConflict
		}
		if stored.Resource != "" && link.requirement.Resource != "" && !exactURLEqual(stored.Resource, link.requirement.Resource) {
			s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_link_failed", "resource_conflict")
			return errMCPResourceConflict
		}
	}
	next := &OAuthToken{
		Username:         canonicalUserID,
		Provider:         storageKey,
		AccessToken:      strings.TrimSpace(token.AccessToken),
		IDToken:          grant.idToken,
		RefreshToken:     strings.TrimSpace(token.RefreshToken),
		ExpiresAt:        grant.accessExpiresAt,
		Issuer:           grant.issuer,
		Resource:         strings.TrimSpace(link.requirement.Resource),
		Scopes:           grant.scopes,
		TokenType:        string(link.requirement.TokenType),
		Subject:          grant.subject,
		ProviderRef:      link.resolved.refKey,
		ClientRef:        link.resolved.clientName,
		IDTokenExpiresAt: grant.idTokenExpiresAt,
		IssuedAt:         now,
	}
	if err := resolver.store.Put(ctx, next); err != nil {
		s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_link_failed", "persist_failed")
		return errMCPLinkFailed
	}
	resolver.clearCooldown(canonicalUserID, storageKey)
	notifyMCPAuthChange(s.cfg, MCPAuthChangeEvent{
		Kind:            "linked",
		CanonicalUserID: canonicalUserID,
		EffectiveUserID: effectiveUserID,
		ServerName:      link.serverName,
		ProviderRef:     link.resolved.refKey,
		StorageKey:      storageKey,
	})
	s.auditLink(ctx, canonicalUserID, link.serverName, link.resolved.refKey, "mcp_auth_link_succeeded", "linked")
	return nil
}

// revokeBestEffort attempts provider-side revocation of the refresh (and
// access) token through the discovered revocation endpoint. Failures are
// audited only — local deletion has already happened.
func (s *mcpLinkService) revokeBestEffort(ctx context.Context, resolved *resolvedProvider, stored *OAuthToken) {
	if s == nil || resolved == nil || stored == nil {
		return
	}
	endpoint := s.discoverRevocationEndpoint(ctx, resolved)
	if endpoint == "" {
		authlog.Log(ctx, authlog.Event{
			Op:             "mcp_auth_revocation_not_supported",
			UserID:         stored.Username,
			Provider:       resolved.refKey,
			Classification: "revocation",
			Action:         "local_delete_only",
		})
		return
	}
	oauthCfg, err := s.loadClientConfig(ctx, resolved.client.ConfigURL)
	if err != nil || oauthCfg == nil {
		return
	}
	revoke := func(tokenValue, hint string) {
		tokenValue = strings.TrimSpace(tokenValue)
		if tokenValue == "" {
			return
		}
		form := url.Values{}
		form.Set("token", tokenValue)
		form.Set("token_type_hint", hint)
		request, rErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if rErr != nil {
			return
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.SetBasicAuth(url.QueryEscape(oauthCfg.ClientID), url.QueryEscape(oauthCfg.ClientSecret))
		httpClient := s.httpClient
		if httpClient == nil {
			httpClient = &http.Client{Timeout: 10 * time.Second}
		}
		if response, dErr := httpClient.Do(request); dErr == nil {
			_ = response.Body.Close()
		}
	}
	revoke(stored.RefreshToken, "refresh_token")
	revoke(stored.AccessToken, "access_token")
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_revocation_attempted",
		UserID:         stored.Username,
		Provider:       resolved.refKey,
		Classification: "revocation",
		Action:         "best_effort",
	})
}

func (s *mcpLinkService) discoverRevocationEndpoint(ctx context.Context, resolved *resolvedProvider) string {
	if resolved == nil || resolved.provider == nil {
		return ""
	}
	issuer := strings.TrimSpace(resolved.provider.Issuer)
	if issuer == "" {
		return ""
	}
	discoveryURL, err := openIDDiscoveryURL(issuer)
	if err != nil {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return ""
	}
	httpClient := s.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ""
	}
	endpoint := strings.TrimSpace(payload.RevocationEndpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, pErr := url.Parse(endpoint); pErr != nil || (parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname())) {
		return ""
	}
	return endpoint
}
