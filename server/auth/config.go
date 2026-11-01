package auth

import (
	streamauth "github.com/viant/jsonrpc/transport/server/auth"
	"github.com/viant/mcp-protocol/authorization"
	"golang.org/x/oauth2"
)

// Config is used to configure the auth server
type Config struct {
	// RequireResourceAuthorization protects static content which has no business
	// handler to verify forwarded credentials. Skills metadata always requires it.
	RequireResourceAuthorization bool
	AuthorizeResource            ResourceAuthorizer
	Policy                       *authorization.Policy
	BackendForFrontend           *BackendForFrontend
	// BFFGrantStore supplies a store outside a listener-bound HTTP request.
	// The HTTP transport's binding is authoritative when present; otherwise
	// this explicit store, then legacy process defaults, remain in effect.
	BFFGrantStore streamauth.Store
	MediationMode string //HTTP, JSONRPC

}

func (c *Config) IsJSONRPCMediationMode() bool {
	return c.MediationMode == "jsonrpc"
}

// BackendForFrontend is used to support the backend-to-frontend flow
type BackendForFrontend struct {
	Client                      *oauth2.Config
	RedirectURI                 string
	AuthorizationExchangeHeader string
}
