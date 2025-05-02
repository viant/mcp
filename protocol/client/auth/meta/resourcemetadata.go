package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ProtectedResourceMetadata represents the full JSON object defined in
// RFC 9728 §2 “OAuth 2.0 Protected Resource Metadata”.
//
// Notes
//   - Only the “resource” member is REQUIRED by the RFC; everything else is OPTIONAL.
//   - `omitempty` keeps absent OPTIONAL members out of the marshalled JSON.
//   - `JWKS` is a raw slice of bytes so you can unmarshal it into whatever JSONWebKey-Set
//     library you prefer (e.g. go-jose/v4).  `jwks_uri` and `jwks` MUST NOT both
//     be present in the same document.
//   - `Extra` captures extension parameters so your code continues to round-trip
//     unknown future fields.
type ProtectedResourceMetadata struct {
	// REQUIRED
	Resource string `json:"resource"`

	// OPTIONAL (but very common)
	AuthorizationServers              []string       `json:"authorization_servers,omitempty"`
	JSONWebKeySetURI                  string         `json:"jwks_uri,omitempty"`
	JSONWebKeySet                     *JSONWebKeySet `json:"jwks,omitempty"` // embedded JSONWebKey Set
	ScopesSupported                   []string       `json:"scopes_supported,omitempty"`
	BearerMethodsSupported            []string       `json:"bearer_methods_supported,omitempty"`
	ResourceSigningAlgValuesSupported []string       `json:"resource_signing_alg_values_supported,omitempty"`

	// Human-readable & docs (all OPTIONAL / i18n-capable)
	ResourceName          string `json:"resource_name,omitempty"`
	ResourceDocumentation string `json:"resource_documentation,omitempty"`
	ResourcePolicyURI     string `json:"resource_policy_uri,omitempty"`
	ResourceTOSURI        string `json:"resource_tos_uri,omitempty"`

	// MTLS / Authz-Details / DPoP (OPTIONAL feature flags)
	TLSClientCertificateBoundAccessTokens bool     `json:"tls_client_certificate_bound_access_tokens,omitempty"`
	AuthorizationDetailsTypesSupported    []string `json:"authorization_details_types_supported,omitempty"`
	DPOPSigningAlgValuesSupported         []string `json:"dpop_signing_alg_values_supported,omitempty"`
	DPOPBoundAccessTokensRequired         bool     `json:"dpop_bound_access_tokens_required,omitempty"`

	// Catch-all for any future or proprietary fields
	Extra map[string]any `json:"-"`
}

// FetchProtectedResourceMetadata fetches the protected resource metadata from the given URL.
func FetchProtectedResourceMetadata(ctx context.Context, metadataURL string, client *http.Client) (*ProtectedResourceMetadata, error) {
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var resource ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&resource); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	if len(resource.AuthorizationServers) == 0 {
		return nil, errors.New("protected resource metadata has no authorization_servers")
	}
	return &resource, nil
}
