package auth

import (
	"context"
	streamauth "github.com/viant/jsonrpc/transport/server/auth"
	"sync"
	"time"
)

type bffTransportKey struct{}
type bffTransport struct {
	store      streamauth.Store
	cookieName string
}

// WithBFFTransport binds the actual HTTP listener's grant store and cookie.
// Only trusted transport setup supplies this context value; wire metadata does not.
func WithBFFTransport(ctx context.Context, store streamauth.Store, cookieName string) context.Context {
	return context.WithValue(ctx, bffTransportKey{}, bffTransport{store: store, cookieName: cookieName})
}

// Defaults used to integrate MCP auth middleware with jsonrpc transport BFF auth cookie handling.
var (
	defaultBFFMu             sync.RWMutex
	defaultBFFGrantStore     streamauth.Store
	defaultBFFAuthCookieName = "BFF-Auth-Session"
)

// SetDefaultBFFAuthStore sets the shared auth grant store used by the auth middleware
// to mint or touch BFF auth cookies. Typically provided by the jsonrpc transport setup.
func SetDefaultBFFAuthStore(store streamauth.Store) {
	defaultBFFMu.Lock()
	defer defaultBFFMu.Unlock()
	defaultBFFGrantStore = store
}

// SetDefaultBFFAuthCookieName sets the default cookie name for the BFF auth session id.
func SetDefaultBFFAuthCookieName(name string) {
	if name != "" {
		defaultBFFMu.Lock()
		defer defaultBFFMu.Unlock()
		defaultBFFAuthCookieName = name
	}
}

func currentBFFDefaults() (streamauth.Store, string) {
	defaultBFFMu.RLock()
	defer defaultBFFMu.RUnlock()
	return defaultBFFGrantStore, defaultBFFAuthCookieName
}

func ensureDefaultBFFStore() streamauth.Store {
	defaultBFFMu.Lock()
	defer defaultBFFMu.Unlock()
	if defaultBFFGrantStore == nil {
		defaultBFFGrantStore = streamauth.NewMemoryStore(30*time.Minute, 24*time.Hour, 2*time.Minute)
	}
	return defaultBFFGrantStore
}
