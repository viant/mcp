package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp/client/auth/transport"
	"net/http"
	"strings"
)

// AuthServer acts as a broker between clients and external OAuth2/OIDC providers.
type AuthServer struct {
	// Policy holds the OAuth2 protection settings; use Global or Tools for spec-based or experimental modes.
	Policy *authorization.Policy

	//if this option is set, server will start oauth 2.1 flow itself (for case we want flexibility with stdio server)
	RoundTripper *transport.RoundTripper
}

// NewAuthServer initializes an AuthServer with the given configuration.
func NewAuthServer(policy *authorization.Policy) (*AuthServer, error) {
	s := &AuthServer{
		Policy: policy,
	}
	return s, nil
}

// MustNewAuthServer creates an AuthServer or panics if configuration is invalid.
func MustNewAuthServer(policy *authorization.Policy) *AuthServer {
	s, err := NewAuthServer(policy)
	if err != nil {
		panic(err)
	}
	return s
}

// RegisterHandlers registers HTTP handlers for OAuth2 endpoints onto the given ServeMux.
func (s *AuthServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.protectedResourcesHandler)
}

// Middleware wraps a handler to enforce bearer-token authorization.
func (s *AuthServer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Policy.Global == nil {
			next.ServeHTTP(w, r)
			return
		}
		if s.Policy.ExcludeURI != "" && strings.HasPrefix(r.URL.Path, s.Policy.ExcludeURI) {
			next.ServeHTTP(w, r)
			return
		}

		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			// Successful bearer token provided -> pass through
			token := &authorization.Token{Token: auth}
			nextReq := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
			next.ServeHTTP(w, nextReq)
			return
		}
		proto, host := extractProtoAndHost(r)
		metaURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", proto, host)
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, metaURL))
		w.WriteHeader(http.StatusUnauthorized)
	})
}

// protectedResourcesHandler serves the OAuth2 authorization server metadata document.
func (s *AuthServer) protectedResourcesHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Policy.Global.ProtectedResourceMetadata)
}
