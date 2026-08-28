package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/viant/mcp-protocol/oauth2/meta"
)

// Discovery bounds for protected-resource metadata fetches. All generic
// networking performed by this package is bounded: strict timeout, limited
// redirects and a capped response size.
const (
	defaultDiscoveryTimeout   = 10 * time.Second
	defaultDiscoveryRedirects = 3
	defaultDiscoveryMaxBytes  = 1 << 20 // 1 MiB
)

// DiscoveryOptions bounds protected-resource metadata fetches.
type DiscoveryOptions struct {
	// Timeout caps the whole fetch; defaults to 10s.
	Timeout time.Duration
	// MaxRedirects caps redirect following; defaults to 3.
	MaxRedirects int
	// MaxBodyBytes caps the decoded response size; defaults to 1 MiB.
	MaxBodyBytes int64
	// AllowHTTP permits plain http metadata URLs (tests/dev only); production
	// hosts must leave this false so only HTTPS metadata is trusted.
	AllowHTTP bool
	// ExpectedOrigin pins the metadata fetch to the MCP/resource origin: the
	// initial metadata URL and every redirect must match its parsed scheme
	// and host exactly. Hosts must set it to the protected-resource / MCP
	// transport origin whenever the metadata URL is taken from an untrusted
	// WWW-Authenticate challenge, so a hostile challenge cannot steer the
	// client into fetching arbitrary URLs (SSRF). It is required by default:
	// discovery fails closed when it is empty unless AllowCrossOrigin is set.
	ExpectedOrigin string
	// AllowCrossOrigin is the explicit host-approved opt-out of the
	// ExpectedOrigin pin. Only when it is true may ExpectedOrigin be left
	// empty; an empty ExpectedOrigin alone never silently approves a fetch.
	AllowCrossOrigin bool
	// Transport overrides the HTTP transport used for the fetch.
	Transport http.RoundTripper
}

func (o *DiscoveryOptions) normalize() DiscoveryOptions {
	result := DiscoveryOptions{}
	if o != nil {
		result = *o
	}
	if result.Timeout <= 0 {
		result.Timeout = defaultDiscoveryTimeout
	}
	if result.MaxRedirects <= 0 {
		result.MaxRedirects = defaultDiscoveryRedirects
	}
	if result.MaxBodyBytes <= 0 {
		result.MaxBodyBytes = defaultDiscoveryMaxBytes
	}
	return result
}

// DiscoverProtectedResource fetches and decodes RFC 9728 protected-resource
// metadata from metadataURL with bounded timeout, redirects and body size.
// Unless options.AllowHTTP is set, metadataURL must be HTTPS. Discovery fails
// closed: options.ExpectedOrigin is required — the initial URL and every
// redirect must match its parsed scheme and host exactly — unless the caller
// explicitly opts out with options.AllowCrossOrigin.
func DiscoverProtectedResource(ctx context.Context, metadataURL string, options *DiscoveryOptions) (*meta.ProtectedResourceMetadata, error) {
	bounded := options.normalize()
	if bounded.ExpectedOrigin == "" && !bounded.AllowCrossOrigin {
		return nil, fmt.Errorf("protected resource metadata discovery requires ExpectedOrigin (set AllowCrossOrigin to explicitly permit cross-origin fetches)")
	}
	parsed, err := url.Parse(metadataURL)
	if err != nil {
		return nil, fmt.Errorf("invalid protected resource metadata URL %q: %w", metadataURL, err)
	}
	if parsed.Scheme != "https" && !bounded.AllowHTTP {
		return nil, fmt.Errorf("protected resource metadata URL %q must use https", metadataURL)
	}
	var expected *url.URL
	if bounded.ExpectedOrigin != "" {
		expected, err = url.Parse(bounded.ExpectedOrigin)
		if err != nil {
			return nil, fmt.Errorf("invalid expected origin %q: %w", bounded.ExpectedOrigin, err)
		}
		if expected.Scheme == "" || expected.Host == "" {
			return nil, fmt.Errorf("expected origin %q must be an absolute URL with scheme and host", bounded.ExpectedOrigin)
		}
		if parsed.Scheme != expected.Scheme || parsed.Host != expected.Host {
			return nil, fmt.Errorf("protected resource metadata URL %q origin does not match expected origin %q", metadataURL, bounded.ExpectedOrigin)
		}
	}
	client := &http.Client{
		Timeout:   bounded.Timeout,
		Transport: bounded.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= bounded.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", bounded.MaxRedirects)
			}
			if req.URL.Scheme != "https" && !bounded.AllowHTTP {
				return fmt.Errorf("redirect to non-https URL %q", req.URL)
			}
			if expected != nil && (req.URL.Scheme != expected.Scheme || req.URL.Host != expected.Host) {
				return fmt.Errorf("redirect to %q violates expected origin %q", req.URL, bounded.ExpectedOrigin)
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(ctx, bounded.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch protected resource metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("protected resource metadata fetch returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, bounded.MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read protected resource metadata: %w", err)
	}
	if int64(len(body)) > bounded.MaxBodyBytes {
		return nil, fmt.Errorf("protected resource metadata exceeds %d bytes", bounded.MaxBodyBytes)
	}
	var resource meta.ProtectedResourceMetadata
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("failed to decode protected resource metadata: %w", err)
	}
	if len(resource.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("protected resource metadata has no authorization_servers")
	}
	return &resource, nil
}

// ChallengeMetadataURL extracts the resource_metadata parameter from a
// WWW-Authenticate challenge header value, e.g.
//
//	Bearer resource_metadata="https://host/.well-known/oauth-protected-resource"
//
// Scheme and parameter names are matched case-insensitively (RFC 9110 §11.1)
// and the parameter value may be quoted or unquoted. It returns an empty
// string when the header is not a Bearer challenge or the parameter is
// absent; it never returns header content through an error.
func ChallengeMetadataURL(wwwAuthenticate string) string {
	value := strings.TrimSpace(wwwAuthenticate)
	if value == "" {
		return ""
	}
	// Strip the auth scheme token when present (a scheme token carries no
	// "="); resource_metadata is only meaningful on a Bearer challenge.
	if idx := strings.IndexAny(value, " \t"); idx > 0 && !strings.Contains(value[:idx], "=") {
		if !strings.EqualFold(value[:idx], "bearer") {
			return ""
		}
		value = strings.TrimLeft(value[idx:], " \t")
	}
	for _, part := range splitChallengeParams(value) {
		name, paramValue, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "resource_metadata") {
			continue
		}
		return strings.Trim(strings.TrimSpace(paramValue), `"`)
	}
	return ""
}

// splitChallengeParams splits a challenge parameter list on commas that are
// outside double-quoted strings.
func splitChallengeParams(value string) []string {
	var parts []string
	start := 0
	inQuotes := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				parts = append(parts, strings.TrimSpace(value[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(value[start:]))
}
