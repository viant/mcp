// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"strings"
	"time"

	authmeta "github.com/viant/scy/auth/metadata"
	"golang.org/x/oauth2"
)

const oauthMetadataTimeout = 5 * time.Second

var defaultOAuthMetadataHTTPClient = &http.Client{Timeout: oauthMetadataTimeout}

func fetchIssuerJWKSURL(ctx context.Context, issuer string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if meta, err := authmeta.FetchAuthorizationServerMetadata(ctx, issuer, oauthMetadataHTTPClient(ctx)); err == nil && meta != nil && strings.TrimSpace(meta.JSONWebKeySetURI) != "" {
		return strings.TrimSpace(meta.JSONWebKeySetURI), nil
	}
	discoveryURL, err := openIDDiscoveryURL(issuer)
	if err != nil {
		return "", err
	}
	return fetchOpenIDJWKSURL(ctx, discoveryURL)
}

func fetchOpenIDJWKSURL(ctx context.Context, discoveryURL string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(discoveryURL), nil)
	if err != nil {
		return "", err
	}
	resp, err := oauthMetadataHTTPClient(ctx).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openid discovery returned status %d", resp.StatusCode)
	}
	var payload authmeta.OpenIDConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	jwksURL := strings.TrimSpace(payload.JwksURI)
	if jwksURL == "" {
		return "", fmt.Errorf("openid discovery missing jwks_uri")
	}
	return jwksURL, nil
}

// oauthMetadataHTTPClient keeps verifier discovery bounded during startup.
// A caller-supplied OAuth client remains authoritative for platform policy and
// deterministic tests.
func oauthMetadataHTTPClient(ctx context.Context) *http.Client {
	if ctx != nil {
		if ctxClient, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && ctxClient != nil {
			return ctxClient
		}
	}
	return defaultOAuthMetadataHTTPClient
}

func openIDDiscoveryURL(issuer string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/.well-known/openid-configuration"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
