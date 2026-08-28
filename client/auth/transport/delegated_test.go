package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/mcp/client/auth/config"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
)

// fakeResolver is a stateful CredentialResolver test double.
type fakeResolver struct {
	mu              sync.Mutex
	current         string
	refreshed       string
	resolveErr      error
	refreshErr      error
	resolveCount    int
	refreshCount    int
	invalidateCount int
	requirements    []config.Requirement
}

func (f *fakeResolver) Resolve(_ context.Context, requirement config.Requirement) (*config.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCount++
	f.requirements = append(f.requirements, requirement)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	if f.current == "" {
		return nil, nil
	}
	return &config.Credential{Token: f.current, TokenType: config.TokenTypeAccessToken}, nil
}

func (f *fakeResolver) Refresh(_ context.Context, _ config.Requirement) (*config.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCount++
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	if f.refreshed == "" {
		return nil, nil
	}
	f.current = f.refreshed
	return &config.Credential{Token: f.refreshed, TokenType: config.TokenTypeAccessToken}, nil
}

// Invalidate models a host that drops the whole credential record — including
// its refresh capability — when told the credential is terminally rejected.
// Any implementation that called Invalidate before Refresh would therefore
// lose the ability to refresh, which the count tests below would catch.
func (f *fakeResolver) Invalidate(_ context.Context, _ config.Requirement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidateCount++
	f.current = ""
	f.refreshed = ""
	return nil
}

// countingTransport accepts requests carrying an expected bearer (and,
// optionally, anonymous requests), rejecting everything else with 401.
type countingTransport struct {
	mu          sync.Mutex
	accepted    string
	anonymousOK bool
	requests    []string // Authorization header per request
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	authorization := req.Header.Get("Authorization")
	c.requests = append(c.requests, authorization)
	status := http.StatusUnauthorized
	if authorization == "Bearer "+c.accepted && c.accepted != "" {
		status = http.StatusOK
	}
	if authorization == "" && c.anonymousOK {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("body")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// panicFlow fails the test if the legacy interactive auth flow is ever invoked.
type panicFlow struct{ t *testing.T }

func (p *panicFlow) Token(context.Context, *oauth2.Config, ...flow.Option) (*oauth2.Token, error) {
	p.t.Fatal("legacy interactive auth flow must not run when an external resolver is installed")
	return nil, fmt.Errorf("unreachable")
}

func newDelegatedRoundTripper(t *testing.T, resolver config.CredentialResolver, inner http.RoundTripper) *RoundTripper {
	return newResolutionRoundTripper(t, resolver, inner, config.ResolutionEager)
}

func newChallengeRoundTripper(t *testing.T, resolver config.CredentialResolver, inner http.RoundTripper) *RoundTripper {
	return newResolutionRoundTripper(t, resolver, inner, config.ResolutionChallenge)
}

func newResolutionRoundTripper(t *testing.T, resolver config.CredentialResolver, inner http.RoundTripper, resolution config.Resolution) *RoundTripper {
	requirement := &config.Requirement{
		ServerName:  "dev6",
		ProviderRef: "adelphic-dev6",
		Resource:    "https://mcp6.example.com/mcp",
		Scopes:      []string{"plan:read"},
		TokenType:   config.TokenTypeAccessToken,
		Resolution:  resolution,
	}
	rt, err := New(
		WithCredentialResolver(resolver, requirement),
		WithTransport(inner),
		WithAuthFlow(&panicFlow{t: t}),
	)
	assert.NoError(t, err)
	return rt
}

func newRequest(t *testing.T, ctx context.Context) *http.Request {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://mcp6.example.com/mcp", strings.NewReader("{}"))
	assert.NoError(t, err)
	return req
}

func TestDelegated_PreflightResolveAndAttach(t *testing.T) {
	resolver := &fakeResolver{current: "good"}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, 1, resolver.resolveCount, "credential resolved proactively before the request")
	assert.Equal(t, 0, resolver.refreshCount)
	assert.Equal(t, []string{"Bearer good"}, inner.requests)
	// the compiled requirement is passed to the resolver verbatim
	assert.Equal(t, "dev6", resolver.requirements[0].ServerName)
	assert.Equal(t, "adelphic-dev6", resolver.requirements[0].ProviderRef)
}

func TestDelegated_ExactlyOneRefreshOneRetryOn401(t *testing.T) {
	resolver := &fakeResolver{current: "stale", refreshed: "good"}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, 1, resolver.resolveCount)
	assert.Equal(t, 1, resolver.refreshCount, "exactly one refresh")
	assert.Equal(t, 0, resolver.invalidateCount,
		"invalidate must never precede refresh — this fake deletes refresh capability on Invalidate, so any premature call breaks the refresh above")
	assert.Equal(t, []string{"Bearer stale", "Bearer good"}, inner.requests, "one initial request, one retry")
}

func TestDelegated_LinkRequiredAfterRetry401(t *testing.T) {
	resolver := &fakeResolver{current: "stale", refreshed: "still-bad"}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.True(t, config.IsLinkRequired(err), "expected typed link-required error, got %v", err)
	var linkRequired *config.OAuthLinkRequiredError
	assert.True(t, errors.As(err, &linkRequired))
	assert.Equal(t, "dev6", linkRequired.ServerName)
	assert.Equal(t, "adelphic-dev6", linkRequired.ProviderRef)
	assert.Equal(t, 1, resolver.refreshCount, "exactly one refresh, never more")
	assert.Equal(t, []string{"Bearer stale", "Bearer still-bad"}, inner.requests, "exactly one retry, never more")
	assert.Equal(t, 1, resolver.invalidateCount, "invalidated exactly once, after the refreshed credential was rejected")
}

func TestDelegated_TransientRefreshErrorNotLinkRequired(t *testing.T) {
	resolver := &fakeResolver{current: "stale", refreshErr: fmt.Errorf("idp temporarily unavailable")}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.False(t, config.IsLinkRequired(err), "transient refresh error must propagate unchanged, got %v", err)
	assert.True(t, strings.Contains(err.Error(), "idp temporarily unavailable"), "error preserved: %v", err)
	assert.Equal(t, 1, resolver.refreshCount)
	assert.Equal(t, 0, resolver.invalidateCount, "refresh failure alone does not invalidate the stored credential")
	assert.Equal(t, []string{"Bearer stale"}, inner.requests, "no retry without refreshed credential")
}

func TestDelegated_TypedRefreshLinkRequiredPropagates(t *testing.T) {
	original := &config.OAuthLinkRequiredError{ServerName: "dev6", ProviderRef: "adelphic-dev6", Cause: fmt.Errorf("invalid_grant")}
	resolver := &fakeResolver{current: "stale", refreshErr: original}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	var linkRequired *config.OAuthLinkRequiredError
	assert.True(t, errors.As(err, &linkRequired))
	assert.Same(t, original, linkRequired, "resolver-produced link-required error propagates unchanged")
	assert.Equal(t, 1, resolver.refreshCount)
	assert.Equal(t, []string{"Bearer stale"}, inner.requests, "no retry without refreshed credential")
}

func TestDelegated_TransientResolveErrorNotLinkRequired(t *testing.T) {
	resolveErr := fmt.Errorf("credential store unavailable")
	resolver := &fakeResolver{resolveErr: resolveErr}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.False(t, config.IsLinkRequired(err), "transient resolve error must propagate unchanged, got %v", err)
	assert.ErrorIs(t, err, resolveErr)
	assert.Empty(t, inner.requests, "no request without a credential")
}

func TestDelegated_LinkRequiredWhenRefreshReturnsNoToken(t *testing.T) {
	resolver := &fakeResolver{current: "stale"}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.True(t, config.IsLinkRequired(err))
	assert.Equal(t, 1, resolver.refreshCount)
	assert.Equal(t, []string{"Bearer stale"}, inner.requests)
}

func TestDelegated_ResolverLinkRequiredPropagates(t *testing.T) {
	original := &config.OAuthLinkRequiredError{ServerName: "dev6", ProviderRef: "adelphic-dev6", Resource: "https://mcp6.example.com/mcp"}
	resolver := &fakeResolver{resolveErr: original}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	var linkRequired *config.OAuthLinkRequiredError
	assert.True(t, errors.As(err, &linkRequired))
	assert.Same(t, original, linkRequired, "resolver-produced link-required error propagates unchanged")
	assert.Empty(t, inner.requests, "no request without a credential")
}

func TestDelegated_LinkRequiredWhenResolveReturnsNoCredential(t *testing.T) {
	resolver := &fakeResolver{}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.True(t, config.IsLinkRequired(err))
	assert.Empty(t, inner.requests)
}

func TestDelegated_ContextUserTokenNeverForwarded(t *testing.T) {
	resolver := &fakeResolver{current: "resolved-token"}
	inner := &countingTransport{accepted: "resolved-token"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "workspace-user-token")
	response, err := rt.RoundTrip(newRequest(t, ctx))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"Bearer resolved-token"}, inner.requests,
		"only the resolved credential may reach a delegated MCP server")
}

func TestDelegated_OverridesCallerAuthorizationHeader(t *testing.T) {
	resolver := &fakeResolver{current: "resolved-token"}
	inner := &countingTransport{accepted: "resolved-token"}
	rt := newDelegatedRoundTripper(t, resolver, inner)

	req := newRequest(t, context.Background())
	req.Header.Set("Authorization", "Bearer caller-supplied")
	response, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"Bearer resolved-token"}, inner.requests)
}

func TestDelegated_InteractiveFlowsDisabled(t *testing.T) {
	resolver := &fakeResolver{current: "token"}
	rt := newDelegatedRoundTripper(t, resolver, &countingTransport{accepted: "token"})

	assert.True(t, rt.HasCredentialResolver())
	_, err := rt.Token(context.Background(), &http.Response{Header: http.Header{}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")

	_, err = rt.ProtectedResourceToken(context.Background(), nil, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// staticCredentialResolver returns a fixed credential from Resolve/Refresh.
type staticCredentialResolver struct {
	credential *config.Credential
}

func (s *staticCredentialResolver) Resolve(context.Context, config.Requirement) (*config.Credential, error) {
	return s.credential, nil
}
func (s *staticCredentialResolver) Refresh(context.Context, config.Requirement) (*config.Credential, error) {
	return s.credential, nil
}
func (s *staticCredentialResolver) Invalidate(context.Context, config.Requirement) error { return nil }

func TestChallenge_NoEagerResolveWhenServerAcceptsAnonymous(t *testing.T) {
	resolver := &fakeResolver{current: "unused"}
	inner := &countingTransport{anonymousOK: true}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, 0, resolver.resolveCount, "challenge mode must never resolve eagerly")
	assert.Equal(t, 0, resolver.refreshCount)
	assert.Equal(t, 0, resolver.invalidateCount)
	assert.Equal(t, []string{""}, inner.requests, "exactly one unauthenticated request, nothing else")
}

func TestChallenge_ResolveOnlyAfter401ExactCounts(t *testing.T) {
	resolver := &fakeResolver{current: "good"}
	inner := &countingTransport{accepted: "good"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, 1, resolver.resolveCount, "exactly one resolve, after the 401 challenge")
	assert.Equal(t, 0, resolver.refreshCount)
	assert.Equal(t, 0, resolver.invalidateCount)
	assert.Equal(t, []string{"", "Bearer good"}, inner.requests,
		"one unauthenticated request, one authenticated retry")
}

func TestChallenge_LinkRequiredWhenRetryRejected(t *testing.T) {
	resolver := &fakeResolver{current: "bad"}
	inner := &countingTransport{accepted: "good"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.True(t, config.IsLinkRequired(err), "expected typed link-required error, got %v", err)
	assert.Equal(t, 1, resolver.resolveCount, "exactly one resolve")
	assert.Equal(t, 0, resolver.refreshCount, "challenge mode never refreshes")
	assert.Equal(t, 1, resolver.invalidateCount, "invalidated once after the terminal rejection")
	assert.Equal(t, []string{"", "Bearer bad"}, inner.requests, "at most one authenticated retry")
}

func TestChallenge_TransientResolveErrorNotLinkRequired(t *testing.T) {
	resolveErr := fmt.Errorf("credential store unavailable")
	resolver := &fakeResolver{resolveErr: resolveErr}
	inner := &countingTransport{accepted: "good"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.False(t, config.IsLinkRequired(err), "transient resolve error must propagate unchanged, got %v", err)
	assert.ErrorIs(t, err, resolveErr)
	assert.Equal(t, []string{""}, inner.requests, "no authenticated retry after resolve failure")
}

func TestChallenge_TypedResolveLinkRequiredPropagates(t *testing.T) {
	original := &config.OAuthLinkRequiredError{ServerName: "dev6", ProviderRef: "adelphic-dev6"}
	resolver := &fakeResolver{resolveErr: original}
	inner := &countingTransport{accepted: "good"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	var linkRequired *config.OAuthLinkRequiredError
	assert.True(t, errors.As(err, &linkRequired))
	assert.Same(t, original, linkRequired, "resolver-produced link-required error propagates unchanged")
}

func TestChallenge_LinkRequiredWhenNoCredential(t *testing.T) {
	resolver := &fakeResolver{}
	inner := &countingTransport{accepted: "good"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.Nil(t, response)
	assert.True(t, config.IsLinkRequired(err))
	assert.Equal(t, 1, resolver.resolveCount)
	assert.Equal(t, []string{""}, inner.requests, "no authenticated retry without a credential")
}

// TestChallenge_HostTokenNeverLeaks asserts that in challenge mode the first
// request carries no Authorization at all — neither a context-carried host
// user token nor a caller-supplied header may reach the server.
func TestChallenge_HostTokenNeverLeaks(t *testing.T) {
	resolver := &fakeResolver{current: "resolved-token"}
	inner := &countingTransport{accepted: "resolved-token"}
	rt := newChallengeRoundTripper(t, resolver, inner)

	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "workspace-user-token")
	req := newRequest(t, ctx)
	req.Header.Set("Authorization", "Bearer caller-supplied")
	response, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"", "Bearer resolved-token"}, inner.requests,
		"first request must have Authorization explicitly removed; only the resolved credential may follow")
	for _, authorization := range inner.requests {
		assert.NotContains(t, authorization, "workspace-user-token", "host user token must never leak")
		assert.NotContains(t, authorization, "caller-supplied", "caller bearer must never leak")
	}
}

// newChallengeMetadataServer serves an MCP endpoint at /mcp that accepts only
// the supplied bearer and challenges everything else with a WWW-Authenticate
// header referencing the server's own same-origin protected-resource metadata
// endpoint at /.well-known/oauth-protected-resource.
func newChallengeMetadataServer(t *testing.T, accepted string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"resource":              server.URL + "/mcp",
				"authorization_servers": []string{"https://idp-dev6.example.com/"},
				"scopes_supported":      []string{"plan:admin", "plan:read"},
			})
		case "/mcp":
			if accepted != "" && r.Header.Get("Authorization") == "Bearer "+accepted {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="mcp", resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func newChallengeMetadataRoundTripper(t *testing.T, resolver config.CredentialResolver) *RoundTripper {
	requirement := &config.Requirement{
		ServerName:  "dev6",
		ProviderRef: "adelphic-dev6",
		Scopes:      []string{"plan:read"},
		TokenType:   config.TokenTypeAccessToken,
		Resolution:  config.ResolutionChallenge,
	}
	rt, err := New(
		WithCredentialResolver(resolver, requirement),
		WithAuthFlow(&panicFlow{t: t}),
	)
	assert.NoError(t, err)
	return rt
}

// TestChallenge_EnrichesRequirementFromChallengeMetadata asserts that a 401
// challenge referencing same-origin protected-resource metadata enriches the
// per-request requirement passed to Resolve (issuer, resource, metadata URL)
// without mutating the shared requirement and without treating
// scopes_supported as required scopes.
func TestChallenge_EnrichesRequirementFromChallengeMetadata(t *testing.T) {
	server := newChallengeMetadataServer(t, "good")
	defer server.Close()

	resolver := &fakeResolver{current: "good"}
	rt := newChallengeMetadataRoundTripper(t, resolver)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/mcp", strings.NewReader("{}"))
	assert.NoError(t, err)

	response, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	if assert.Len(t, resolver.requirements, 1) {
		enriched := resolver.requirements[0]
		assert.Equal(t, server.URL+"/mcp", enriched.Resource, "resource learned from metadata")
		assert.Equal(t, "https://idp-dev6.example.com", enriched.Issuer, "issuer learned from metadata")
		assert.Equal(t, server.URL+"/.well-known/oauth-protected-resource", enriched.MetadataURL)
		assert.Equal(t, []string{"plan:read"}, enriched.Scopes,
			"scopes_supported is advisory and must never widen required scopes")
	}
	shared := rt.Requirement()
	assert.Equal(t, "", shared.Issuer, "shared requirement must never be mutated by per-request enrichment")
	assert.Equal(t, "", shared.Resource)
	assert.Equal(t, "", shared.MetadataURL)
}

// TestChallenge_LinkRequiredCarriesEnrichedRequirement asserts that the typed
// link-required error produced after challenge enrichment carries the learned
// issuer/resource binding and metadata URL, so a host can persist it.
func TestChallenge_LinkRequiredCarriesEnrichedRequirement(t *testing.T) {
	server := newChallengeMetadataServer(t, "good")
	defer server.Close()

	resolver := &fakeResolver{} // resolves no credential after the challenge
	rt := newChallengeMetadataRoundTripper(t, resolver)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/mcp", strings.NewReader("{}"))
	assert.NoError(t, err)

	response, err := rt.RoundTrip(req)
	assert.Nil(t, response)
	var linkRequired *config.OAuthLinkRequiredError
	if assert.True(t, errors.As(err, &linkRequired), "expected typed link-required error, got %v", err) {
		assert.Equal(t, "dev6", linkRequired.ServerName)
		assert.Equal(t, "adelphic-dev6", linkRequired.ProviderRef)
		assert.Equal(t, "https://idp-dev6.example.com", linkRequired.Issuer)
		assert.Equal(t, server.URL+"/mcp", linkRequired.Resource)
		assert.Equal(t, server.URL+"/.well-known/oauth-protected-resource", linkRequired.MetadataURL)
	}
}

// TestChallenge_ConcurrentEnrichmentNeverMutatesSharedRequirement drives
// concurrent challenge round trips through one transport so the race detector
// can prove enrichment only ever touches per-request requirement clones.
func TestChallenge_ConcurrentEnrichmentNeverMutatesSharedRequirement(t *testing.T) {
	server := newChallengeMetadataServer(t, "good")
	defer server.Close()

	resolver := &fakeResolver{current: "good"}
	rt := newChallengeMetadataRoundTripper(t, resolver)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/mcp", strings.NewReader("{}"))
			assert.NoError(t, err)
			response, err := rt.RoundTrip(req)
			if assert.NoError(t, err) {
				assert.Equal(t, http.StatusOK, response.StatusCode)
				_ = response.Body.Close()
			}
		}()
	}
	wg.Wait()
	shared := rt.Requirement()
	assert.Equal(t, "", shared.Issuer, "shared requirement must never be mutated by per-request enrichment")
	assert.Equal(t, "", shared.MetadataURL)
	for _, requirement := range resolver.requirements {
		assert.Equal(t, server.URL+"/.well-known/oauth-protected-resource", requirement.MetadataURL)
	}
}

// TestChallenge_CrossOriginChallengeMetadataNeverFetched covers the SSRF
// vector: a hostile challenge pointing at a foreign origin must never be
// fetched, and the flow proceeds with the unenriched requirement.
func TestChallenge_CrossOriginChallengeMetadataNeverFetched(t *testing.T) {
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cross-origin challenge metadata URL must never be fetched")
	}))
	defer attacker.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+attacker.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	resolver := &fakeResolver{current: "good"}
	rt := newChallengeMetadataRoundTripper(t, resolver)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/mcp", strings.NewReader("{}"))
	assert.NoError(t, err)

	response, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	if assert.Len(t, resolver.requirements, 1) {
		assert.Equal(t, "", resolver.requirements[0].MetadataURL,
			"rejected cross-origin metadata must leave the requirement unenriched")
		assert.Equal(t, "", resolver.requirements[0].Issuer)
	}
}

func TestDelegated_RejectsCredentialContradictingRequirement(t *testing.T) {
	testCases := []struct {
		name       string
		credential *config.Credential
		expect     string
	}{
		{
			name:       "token type mismatch",
			credential: &config.Credential{Token: "SECRET-VALUE", TokenType: config.TokenTypeIDToken},
			expect:     "tokenType",
		},
		{
			name:       "provider mismatch",
			credential: &config.Credential{Token: "SECRET-VALUE", ProviderRef: "other-provider"},
			expect:     "providerRef",
		},
		{
			name:       "resource mismatch",
			credential: &config.Credential{Token: "SECRET-VALUE", Resource: "https://evil.example.com/mcp"},
			expect:     "resource",
		},
		{
			name:       "expired credential",
			credential: &config.Credential{Token: "SECRET-VALUE", ExpiresAt: time.Now().Add(-time.Minute)},
			expect:     "expired",
		},
		{
			name:       "missing required scope",
			credential: &config.Credential{Token: "SECRET-VALUE", Scopes: []string{"other:scope"}},
			expect:     "scope",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			inner := &countingTransport{accepted: "SECRET-VALUE"}
			rt := newDelegatedRoundTripper(t, &staticCredentialResolver{credential: testCase.credential}, inner)
			response, err := rt.RoundTrip(newRequest(t, context.Background()))
			assert.Nil(t, response)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), testCase.expect)
				assert.NotContains(t, err.Error(), "SECRET-VALUE", "credential value must never appear in errors")
			}
			assert.Empty(t, inner.requests, "a contradictory credential must never be sent")
		})
	}
}

func TestDelegated_AcceptsConsistentCredentialMetadata(t *testing.T) {
	credential := &config.Credential{
		Token:       "good",
		TokenType:   config.TokenTypeAccessToken,
		ProviderRef: "adelphic-dev6",
		Resource:    "https://mcp6.example.com/mcp",
		Scopes:      []string{"plan:read", "plan:create"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	inner := &countingTransport{accepted: "good"}
	rt := newDelegatedRoundTripper(t, &staticCredentialResolver{credential: credential}, inner)
	response, err := rt.RoundTrip(newRequest(t, context.Background()))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

// TestRoundTripper_RequirementReturnsClone asserts callers cannot mutate the
// transport's internal requirement through the accessor.
func TestRoundTripper_RequirementReturnsClone(t *testing.T) {
	rt := newDelegatedRoundTripper(t, &fakeResolver{current: "good"}, &countingTransport{accepted: "good"})

	first := rt.Requirement()
	first.ServerName = "mutated"
	first.Scopes[0] = "mutated:scope"

	second := rt.Requirement()
	assert.Equal(t, "dev6", second.ServerName, "internal requirement must be unaffected by caller mutation")
	assert.Equal(t, []string{"plan:read"}, second.Scopes)
	assert.NotSame(t, first, second)
}

func TestLegacyRoundTrip_UnaffectedWithoutResolver(t *testing.T) {
	inner := &countingTransport{accepted: "ctx-token"}
	rt, err := New(WithTransport(inner))
	assert.NoError(t, err)
	assert.False(t, rt.HasCredentialResolver())

	// legacy path still forwards the context token on the first probe
	ctx := context.WithValue(context.Background(), ContextAuthTokenKey, "ctx-token")
	response, err := rt.RoundTrip(newRequest(t, ctx))
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"Bearer ctx-token"}, inner.requests)
}
