package mock

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
)

// AuthorizationService is a test server that simulates an OAuth2 authorization server
type AuthorizationService struct {
	PrivateKey              *rsa.PrivateKey
	Issuer                  string
	ClientID                string
	ClientSecret            string
	AuthorizedScopes        []string
	TokenHandler            func(w http.ResponseWriter, r *http.Request)
	AuthorizeHandler        func(w http.ResponseWriter, r *http.Request)
	MetadataHandler         func(w http.ResponseWriter, r *http.Request)
	ResourceHandler         func(w http.ResponseWriter, r *http.Request)
	ResourceMetadataHandler func(w http.ResponseWriter, r *http.Request)
	// JwksHandler handles requests for the JSON Web Key Set
	JwksHandler func(w http.ResponseWriter, r *http.Request)
}

// NewAuthorizationService creates a new mock OAuth2 authorization server
func NewAuthorizationService(opts ...Option) (*AuthorizationService, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %v", err)
	}

	service := &AuthorizationService{
		PrivateKey:       privateKey,
		ClientID:         "test_client_id",
		ClientSecret:     "test_client_secret",
		AuthorizedScopes: []string{"openid", "profile", "email"},
	}
	for _, opt := range opts {
		opt(service)
	}
	return service, nil
}

// Register registers HTTP handlers for all mock endpoints onto the given ServeMux.
func (m *AuthorizationService) Register(mux *http.ServeMux) {
	mux.Handle("/", &Handler{Server: m})
}

// Handler returns an http.Handler for all mock endpoints, suitable for any HTTP server.
func (m *AuthorizationService) Handler() http.Handler {
	mux := http.NewServeMux()
	m.Register(mux)
	return mux
}
