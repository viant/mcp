package auth

import streamauth "github.com/viant/jsonrpc/transport/server/auth"

// Defaults used to integrate MCP auth middleware with jsonrpc transport BFF auth cookie handling.
var (
	defaultBFFGrantStore     streamauth.Store
	defaultBFFAuthCookieName = "BFF-Auth-Session"
)

// SetDefaultBFFAuthStore sets the shared auth grant store used by the auth middleware
// to mint or touch BFF auth cookies. Typically provided by the jsonrpc transport setup.
func SetDefaultBFFAuthStore(store streamauth.Store) {
	defaultBFFGrantStore = store
}

// SetDefaultBFFAuthCookieName sets the default cookie name for the BFF auth session id.
func SetDefaultBFFAuthCookieName(name string) {
	if name != "" {
		defaultBFFAuthCookieName = name
	}
}
