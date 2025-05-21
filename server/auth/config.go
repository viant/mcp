package auth

import (
	"github.com/viant/mcp-protocol/authorization"
	"golang.org/x/oauth2"
)

// Config is used to configure the auth server
type Config struct {
	Policy             *authorization.Policy
	BackendForFrontend *BackendForFrontend
	MediationMode      string //HTTP, JSONRPC

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
