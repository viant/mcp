package transport

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/internal/debuglog"
)

// delegatedRoundTrip is the single transport-level owner of credential
// attachment and 401 recovery when an external CredentialResolver is
// installed. The resolved credential is attached only to the outbound request
// clone; it is never stored in the request context or any host identity
// state. Legacy interactive/browser and BFF fallbacks never run on this path.
//
// Ordinary resolver errors (transient IDP/storage failures) propagate
// unchanged; a typed *config.OAuthLinkRequiredError propagates as-is; and
// link-required is synthesized only when the resolver returns no credential
// or the resolved/refreshed credential is rejected with 401.
//
// Eager mode (default): one proactive resolve, one initial request; on 401
// one refresh (never preceded by Invalidate — Refresh must retain the stored
// refresh credential) and one retry; Invalidate fires only after the
// refreshed credential is rejected, then a typed
// *config.OAuthLinkRequiredError is returned.
//
// Challenge mode (ResolutionChallenge): the first request is sent with
// Authorization explicitly removed and no resolver call; on 401 the
// requirement clone is enriched from challenge-referenced protected-resource
// metadata (pinned to the request origin), then one resolve and at most one
// authenticated retry; Invalidate fires only after that retry is rejected,
// then a typed *config.OAuthLinkRequiredError is returned.
func (r *RoundTripper) delegatedRoundTrip(req *http.Request) (*http.Response, error) {
	if r.requirement != nil && r.requirement.Resolution == config.ResolutionChallenge {
		return r.challengeRoundTrip(req)
	}
	return r.eagerRoundTrip(req)
}

// requirementClone returns a per-request deep copy of the compiled
// requirement so concurrent round trips can never observe each other's
// mutations (e.g. challenge enrichment).
func (r *RoundTripper) requirementClone() *config.Requirement {
	if requirement := r.requirement.Clone(); requirement != nil {
		return requirement
	}
	return &config.Requirement{}
}

// eagerRoundTrip resolves a credential proactively before the request and
// coordinates the single-refresh/single-retry 401 recovery.
func (r *RoundTripper) eagerRoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	requirement := r.requirementClone()
	credential, err := r.credentialResolver.Resolve(ctx, *requirement)
	if err != nil {
		return nil, err
	}
	if credential == nil || credential.Token == "" {
		return nil, config.NewLinkRequired(requirement, nil)
	}
	if err := r.verifyCredential(requirement, credential); err != nil {
		return nil, err
	}
	response, err := r.sendWithCredential(req, credential)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusUnauthorized {
		return response, nil
	}
	debuglog.Printf("[auth-rt] delegated 401 url=%q, refreshing once", req.URL.String())
	_ = response.Body.Close()
	// Refresh must not be preceded by Invalidate: Refresh bypasses stale
	// access-token caches by contract while the stored refresh credential is
	// retained. Invalidate fires only after the refreshed credential is
	// rejected below.
	refreshed, err := r.credentialResolver.Refresh(ctx, *requirement)
	if err != nil {
		return nil, err
	}
	if refreshed == nil || refreshed.Token == "" {
		return nil, config.NewLinkRequired(requirement, fmt.Errorf("credential refresh returned no token"))
	}
	if err := r.verifyCredential(requirement, refreshed); err != nil {
		return nil, err
	}
	retry, err := r.sendWithCredential(req, refreshed)
	if err != nil {
		return nil, err
	}
	if retry.StatusCode == http.StatusUnauthorized {
		_ = retry.Body.Close()
		if err := r.credentialResolver.Invalidate(ctx, *requirement); err != nil {
			debuglog.Printf("[auth-rt] delegated invalidate failed: %v", err)
		}
		return nil, config.NewLinkRequired(requirement, fmt.Errorf("request rejected with 401 after refresh"))
	}
	return retry, nil
}

// challengeRoundTrip implements ResolutionChallenge: the first request goes
// out with Authorization explicitly removed (so no host or caller token can
// leak), the resolver is consulted only after a 401 challenge — with the
// requirement clone enriched from challenge-referenced protected-resource
// metadata — and at most one authenticated retry follows.
func (r *RoundTripper) challengeRoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	requirement := r.requirementClone()
	response, err := r.sendUnauthenticated(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusUnauthorized {
		return response, nil
	}
	debuglog.Printf("[auth-rt] delegated challenge 401 url=%q, resolving once", req.URL.String())
	r.enrichFromChallenge(ctx, req, response, requirement)
	_ = response.Body.Close()
	credential, err := r.credentialResolver.Resolve(ctx, *requirement)
	if err != nil {
		return nil, err
	}
	if credential == nil || credential.Token == "" {
		return nil, config.NewLinkRequired(requirement, nil)
	}
	if err := r.verifyCredential(requirement, credential); err != nil {
		return nil, err
	}
	retry, err := r.sendWithCredential(req, credential)
	if err != nil {
		return nil, err
	}
	if retry.StatusCode == http.StatusUnauthorized {
		_ = retry.Body.Close()
		if err := r.credentialResolver.Invalidate(ctx, *requirement); err != nil {
			debuglog.Printf("[auth-rt] delegated invalidate failed: %v", err)
		}
		return nil, config.NewLinkRequired(requirement, fmt.Errorf("request rejected with 401 after challenge resolution"))
	}
	return retry, nil
}

// enrichFromChallenge applies RFC 9728 protected-resource metadata referenced
// by a 401 challenge to the per-request requirement clone, so the resolver —
// and any link-required error built from the clone — carries the learned
// issuer/resource binding and the metadata URL. The metadata fetch is pinned
// to the MCP request origin (a hostile challenge can never steer it
// cross-origin); scopes_supported is advisory and never merged into required
// scopes. Enrichment failures leave the requirement unchanged.
func (r *RoundTripper) enrichFromChallenge(ctx context.Context, req *http.Request, response *http.Response, requirement *config.Requirement) {
	metadataURL := config.ChallengeMetadataURL(response.Header.Get("WWW-Authenticate"))
	if metadataURL == "" {
		return
	}
	metadata, err := config.DiscoverProtectedResource(ctx, metadataURL, &config.DiscoveryOptions{
		ExpectedOrigin: req.URL.Scheme + "://" + req.URL.Host,
		// The metadata fetch is same-origin with the MCP transport, so plain
		// http is acceptable only when the transport itself uses it.
		AllowHTTP: req.URL.Scheme == "http",
		Transport: r.transport,
	})
	if err != nil {
		debuglog.Printf("[auth-rt] challenge metadata discovery skipped: %v", err)
		return
	}
	// Apply on a scratch clone so a mismatch cannot leave the requirement
	// partially rewritten.
	enriched := requirement.Clone()
	if err := enriched.ApplyProtectedResourceMetadata(metadata); err != nil {
		debuglog.Printf("[auth-rt] challenge metadata inconsistent with requirement: %v", err)
		return
	}
	enriched.MetadataURL = metadataURL
	*requirement = *enriched
}

// verifyCredential rejects resolver-returned credentials whose metadata
// contradicts the requirement. The error never carries the credential token
// value.
func (r *RoundTripper) verifyCredential(requirement *config.Requirement, credential *config.Credential) error {
	if err := credential.Verify(requirement, time.Now()); err != nil {
		return fmt.Errorf("resolver returned credential inconsistent with requirement: %w", err)
	}
	return nil
}

// sendUnauthenticated replays req with the Authorization header explicitly
// removed so a host user token or caller-supplied bearer can never leak to a
// delegated MCP server before the 401 challenge.
func (r *RoundTripper) sendUnauthenticated(req *http.Request) (*http.Response, error) {
	attempt := clone(req)
	attempt.Header.Del("Authorization")
	if r.jar != nil {
		for _, cookie := range r.jar.Cookies(attempt.URL) {
			attempt.AddCookie(cookie)
		}
	}
	response, err := r.transport.RoundTrip(attempt)
	if err != nil {
		return nil, err
	}
	if r.jar != nil {
		r.jar.SetCookies(attempt.URL, response.Cookies())
	}
	return response, nil
}

// sendWithCredential replays req with the resolved credential attached as the
// sole Authorization value, overriding any caller-supplied bearer so a host
// user token can never leak to a delegated MCP server.
func (r *RoundTripper) sendWithCredential(req *http.Request, credential *config.Credential) (*http.Response, error) {
	attempt := clone(req)
	if r.jar != nil {
		for _, cookie := range r.jar.Cookies(attempt.URL) {
			attempt.AddCookie(cookie)
		}
	}
	attempt.Header.Set("Authorization", "Bearer "+credential.Token)
	response, err := r.transport.RoundTrip(attempt)
	if err != nil {
		return nil, err
	}
	if r.jar != nil {
		r.jar.SetCookies(attempt.URL, response.Cookies())
	}
	return response, nil
}
