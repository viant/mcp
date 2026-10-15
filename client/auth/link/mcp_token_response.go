package link

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/link/provider"
	"github.com/viant/mcp/client/auth/link/providerregistry"
	"golang.org/x/oauth2"
)

// tokenResponsePolicy applies only to grants fetched by the server from the
// configured token endpoint. Never use it for client-supplied tokens (OOB or
// session import): response extras in those envelopes are not authoritative.
func tokenResponsePolicy(ctx context.Context, registry *providerregistry.Registry, resolved *resolvedProvider, requirement *authcfg.Requirement, cfg *oauth2.Config) (*oauthprovider.TokenResponse, error) {
	if registry == nil {
		return nil, nil
	}
	doc, err := registry.Provider(ctx, resolved.refKey)
	if providerregistry.IsNotFound(err) {
		return nil, nil // Inline providers cannot enable this policy.
	}
	if err != nil {
		return nil, err
	}
	policy := doc.TokenResponse
	if policy == nil {
		return nil, nil
	}
	if doc.Disabled || providerregistry.GloballyDisabled() {
		return nil, fmt.Errorf("token-response provider is disabled")
	}
	if requirement.TokenType != authcfg.TokenTypeAccessToken {
		return nil, fmt.Errorf("token-response grants require tokenType=accessToken")
	}
	if cfg == nil || strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("token-response grants require a confidential OAuth client")
	}
	endpoint, err := url.Parse(policy.TokenURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || cfg.Endpoint.TokenURL != policy.TokenURL {
		return nil, fmt.Errorf("token-response grants require the pinned HTTPS token endpoint")
	}
	if strings.TrimSpace(policy.Resource) == "" || !exactURLEqual(policy.Resource, requirement.Resource) {
		return nil, fmt.Errorf("token-response resource does not match the MCP requirement")
	}
	if authcfg.NormalizeIssuer(requirement.Issuer) != authcfg.NormalizeIssuer(doc.Issuer) || strings.TrimSpace(doc.Issuer) == "" {
		return nil, fmt.Errorf("token-response issuer does not match the provider")
	}
	if strings.TrimSpace(policy.SubjectPath) == "" {
		return nil, fmt.Errorf("token-response subjectPath is required")
	}
	return policy, nil
}

func tokenResponseSubject(token *oauth2.Token, path string) string {
	parts := strings.Split(path, ".")
	var value interface{} = token.Extra(parts[0])
	for _, part := range parts[1:] {
		object, ok := value.(map[string]interface{})
		if !ok {
			return ""
		}
		value = object[part]
	}
	subject, _ := value.(string)
	return strings.TrimSpace(subject)
}

// validateTokenResponse receives only a response from an authenticated server
// exchange. Issuer/resource come from its pinned provider policy, not decoded
// token claims. The subject and lifetime come from the HTTPS token response.
// Refresh responses may omit a subject/scopes, retaining the original grant.
func validateTokenResponse(policy *oauthprovider.TokenResponse, requirement *authcfg.Requirement, token *oauth2.Token, previous *OAuthToken, now time.Time) (*verifiedGrant, error) {
	if token == nil || strings.TrimSpace(token.AccessToken) == "" || !strings.EqualFold(token.TokenType, "bearer") || !token.Expiry.After(now) {
		return nil, fmt.Errorf("token response requires a bearer access token with a future expiration")
	}
	subject := tokenResponseSubject(token, policy.SubjectPath)
	if previous != nil {
		if subject != "" && subject != previous.Subject {
			return nil, fmt.Errorf("token refresh changed the provider subject")
		}
		if subject == "" {
			subject = previous.Subject
		}
	}
	if subject == "" {
		return nil, fmt.Errorf("token response carries no provider subject at %q", policy.SubjectPath)
	}
	scopes, present := oauthResponseScopes(token)
	// An explicit empty scope is a reduction, not an omitted response field.
	present = present || token.Extra("scope") != nil
	if !present && previous != nil {
		scopes = previous.Scopes
	}
	if !scopesCover(scopes, requirement.Scopes) {
		return nil, fmt.Errorf("token response scopes do not cover the MCP requirement")
	}
	return &verifiedGrant{
		issuer: authcfg.NormalizeIssuer(requirement.Issuer), subject: subject,
		scopes: normalizeScopes(scopes), accessExpiresAt: token.Expiry,
	}, nil
}

// Keep the endpoint pin effective across HTTP redirects. Preserve configured
// transports (including test transports) while rejecting token redirects.
func pinnedTokenExchangeContext(ctx context.Context) context.Context {
	client := &http.Client{Timeout: 30 * time.Second}
	if configured, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && configured != nil {
		copy := *configured
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}
