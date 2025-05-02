package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AuthServer acts as a broker between clients and external OAuth2/OIDC providers.
type AuthServer struct {
	Config *Config
}

// NewAuthServer initializes an AuthServer with the given configuration.
func NewAuthServer(config *Config) (*AuthServer, error) {
	s := &AuthServer{
		Config: config,
	}
	return s, nil
}

// MustNewAuthServer creates an AuthServer or panics if configuration is invalid.
func MustNewAuthServer(config *Config) *AuthServer {
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
			next.ServeHTTP(w, r)
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
	_ = json.NewEncoder(w).Encode(s.Config.Global)
}
