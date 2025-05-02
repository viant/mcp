package mock

import (
	"encoding/json"
	"github.com/viant/mcp/protocol/client/auth/meta"
	"net/http"
)

// defaultMetadataHandler serves the OAuth2 server metadata at /.well-known/oauth-authorization-server
func (m *AuthorizationService) defaultMetadataHandler(w http.ResponseWriter, _ *http.Request) {
	metadata := meta.AuthorizationServerMetadata{
		Issuer:                            m.Issuer,
		AuthorizationEndpoint:             m.Issuer + "/authorize",
		TokenEndpoint:                     m.Issuer + "/token",
		JSONWebKeySetURI:                  m.Issuer + "/jwks",
		ScopesSupported:                   m.AuthorizedScopes,
		ResponseTypesSupported:            []string{"code", "token", "id_token", "code token", "code id_token", "token id_token", "code token id_token"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"client_secret_basic", "client_secret_post"},
		CodeChallengeMethodsSupported:     []string{"plain", "S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// defaultResourceMetadataHandler handles /resource-metadata requests
func (m *AuthorizationService) defaultResourceMetadataHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resourceMetadata := map[string]interface{}{
		"resource":              m.Issuer + "/resource",
		"authorization_servers": []string{m.Issuer},
		"scopes_supported":      []string{"openid", "profile", "email", "resource"},
	}
	_ = json.NewEncoder(w).Encode(resourceMetadata)
}
