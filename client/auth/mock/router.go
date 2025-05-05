package mock

import (
	"net/http"
)

// Handler routes HTTP requests to the appropriate mock OAuth2 server endpoints.
type Handler struct {
	// Server is the mock authorization server with endpoint handlers.
	Server *AuthorizationService
}

// ServeHTTP dispatches incoming HTTP requests based on URL path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/token":
		if h.Server.TokenHandler != nil {
			h.Server.TokenHandler(w, r)
		} else {
			h.Server.defaultTokenHandler(w, r)
		}
	case "/authorize":
		if h.Server.AuthorizeHandler != nil {
			h.Server.AuthorizeHandler(w, r)
		} else {
			h.Server.defaultAuthorizeHandler(w, r)
		}
	case "/.well-known/oauth-authorization-server":
		if h.Server.MetadataHandler != nil {
			h.Server.MetadataHandler(w, r)
		} else {
			h.Server.defaultMetadataHandler(w, r)
		}
	case "/resource":
		if h.Server.ResourceHandler != nil {
			h.Server.ResourceHandler(w, r)
		} else {
			h.Server.defaultResourceHandler(w, r)
		}
	case "/jwks":
		if h.Server.JwksHandler != nil {
			h.Server.JwksHandler(w, r)
		} else {
			h.Server.defaultJwksHandler(w, r)
		}
	case "/resource-metadata":
		if h.Server.ResourceMetadataHandler != nil {
			h.Server.ResourceMetadataHandler(w, r)
		} else {
			h.Server.defaultResourceMetadataHandler(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
