package auth

import (
	"context"
	"encoding/json"
	"fmt"
	authschema "github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp/client/auth/transport"
	"net/http"
	"strings"
)

// AuthServer acts as a broker between clients and external OAuth2/OIDC providers.
type AuthServer struct {
	// Config holds the OAuth2 protection settings; use Global or Tools for spec-based or experimental modes.
	Config *authschema.Config

	//if this option is set, server will start oauth 2.1 flow itself (for case we want flexibility with stdio server)
	RoundTripper *transport.RoundTripper
}

// NewAuthServer initializes an AuthServer with the given configuration.
func NewAuthServer(config *authschema.Config) (*AuthServer, error) {
	s := &AuthServer{
		Config: config,
	}
	return s, nil
}

// MustNewAuthServer creates an AuthServer or panics if configuration is invalid.
func MustNewAuthServer(config *authschema.Config) *AuthServer {
	s, err := NewAuthServer(config)
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
		if s.Config.Global == nil {
			next.ServeHTTP(w, r)
			return
		}
		if s.Config.ExcludeURI != "" && strings.HasPrefix(r.URL.Path, s.Config.ExcludeURI) {
			next.ServeHTTP(w, r)
			return
		}

		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			// Successful bearer token provided -> pass through
			token := &authschema.Token{Token: auth}
			nextReq := r.WithContext(context.WithValue(r.Context(), authschema.TokenKey, token))
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
	_ = json.NewEncoder(w).Encode(s.Config.Global.ProtectedResourceMetadata)
}
