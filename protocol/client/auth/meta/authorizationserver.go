package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// AuthorizationServerMetadata models the JSON object defined in RFC 8414
// (OAuth 2.0 Authorization Server Metadata).
//
// NOTE:  • All slices are nil by default; add values as needed.
//   - The "Extra" map preserves extension parameters that are
//     not explicitly modelled here.
type AuthorizationServerMetadata struct {
	// REQUIRED
	Issuer                string `json:"issuer"` // Base URL
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `json:"token_endpoint,omitempty"`
	JSONWebKeySetURI      string `json:"jwks_uri,omitempty"`

	// RECOMMENDED
	RegistrationEndpoint string   `json:"registration_endpoint,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`

	// Common OPTIONAL sets
	ResponseTypesSupported                     []string `json:"response_types_supported,omitempty"`
	ResponseModesSupported                     []string `json:"response_modes_supported,omitempty"`
	GrantTypesSupported                        []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	TokenEndpointAuthSigningAlgValuesSupported []string `json:"token_endpoint_auth_signing_alg_values_supported,omitempty"`

	// RFC 8414 §2 – UI / policy pages
	ServiceDocumentation string   `json:"service_documentation,omitempty"`
	UILocalesSupported   []string `json:"ui_locales_supported,omitempty"`
	OPPolicyURI          string   `json:"op_policy_uri,omitempty"`
	OPTosURI             string   `json:"op_tos_uri,omitempty"`

	// RFC 8414 §2 – Revocation & Introspection (RFC 7009 / RFC 7662)
	RevocationEndpoint                        string   `json:"revocation_endpoint,omitempty"`
	RevocationEndpointAuthMethodsSupported    []string `json:"revocation_endpoint_auth_methods_supported,omitempty"`
	RevocationEndpointAuthSigningAlgValues    []string `json:"revocation_endpoint_auth_signing_alg_values_supported,omitempty"`
	IntrospectionEndpoint                     string   `json:"introspection_endpoint,omitempty"`
	IntrospectionEndpointAuthMethodsSupported []string `json:"introspection_endpoint_auth_methods_supported,omitempty"`
	IntrospectionEndpointAuthSigningAlgValues []string `json:"introspection_endpoint_auth_signing_alg_values_supported,omitempty"`

	// JAR, PAR, Device Code, CIBA, etc. (registered extensions)
	PushedAuthorizationRequestEndpoint string   `json:"pushed_authorization_request_endpoint,omitempty"`
	RequirePushedAuthorizationRequests bool     `json:"require_pushed_authorization_requests,omitempty"`
	RequestObjectSigningAlgsSupported  []string `json:"request_object_signing_alg_values_supported,omitempty"`
	DeviceAuthorizationEndpoint        string   `json:"device_authorization_endpoint,omitempty"`
	BackchannelAuthenticationEndpoint  string   `json:"backchannel_authentication_endpoint,omitempty"`
	BackchannelTokenDeliveryModes      []string `json:"backchannel_token_delivery_modes_supported,omitempty"`
	BackchannelAuthRequestSigningAlgs  []string `json:"backchannel_authentication_request_signing_alg_values_supported,omitempty"`
	BackchannelUserCodeParameter       bool     `json:"backchannel_user_code_parameter_supported,omitempty"`

	// OpenID Connect (if the AS is also an OIDC OP)
	IDTokenSigningAlgsSupported []string `json:"id_token_signing_alg_values_supported,omitempty"`
	// (You can add other OIDC discovery fields if your deployment needs them.)

	// Catch-all for undeclared / future metadata
	Extra map[string]any `json:"-"`
}

// FetchAuthorizationServerMetadata fetches the Authorization Server
func FetchAuthorizationServerMetadata(ctx context.Context, issuer string, client *http.Client) (*AuthorizationServerMetadata, error) {
	if client == nil {
		client = http.DefaultClient
	}

	wellKnownURL, err := joinWellKnown(issuer)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var metaDoc AuthorizationServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metaDoc); err != nil {
		return nil, err
	}
	return &metaDoc, nil
}

// joinWellKnown builds “…/.well-known/oauth-authorization-server” as  specified in RFC 8414 §5, preserving any existing issuer path and guaranteeing exactly one “/” separator.
func joinWellKnown(issuer string) (string, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("issuer URL parse error: %w", err)
	}
	// The well-known segment must be relative to the *issuer path* (if any).
	u.Path = strings.TrimRight(u.Path, "/") + "/.well-known/oauth-authorization-server"
	// Queries or fragments are not allowed on the discovery URL.
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
