package store

import (
	"crypto"
	"github.com/viant/afs/http"
	"github.com/viant/afs/url"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"golang.org/x/oauth2"
	"sync"
)

type TokenKey struct {
	Issuer string
	Scopes string
}

// Store is a pluggable persistence layer for tokens & client IDs.
// The in‑memory default is fine for CLI tools; swap with Redis/SQL for fleets.
type Store interface {
	LookupClientConfig(issuer string) (*oauth2.Config, bool)
	AddClientConfig(issuer string, client *oauth2.Config) error
	AddAuthorizationServerMetadata(metadata *meta.AuthorizationServerMetadata) error
	LookupAuthorizationServerMetadata(issuer string) (*meta.AuthorizationServerMetadata, bool)
	AddIssuerPublicKeys(issuer string, keys map[string]crypto.PublicKey) error
	LookupIssuerPublicKeys(issuer string) (map[string]crypto.PublicKey, bool)
	AddToken(key TokenKey, token *oauth2.Token) error
	LookupToken(key TokenKey) (*oauth2.Token, bool)
}

type MemoryStoreOption func(*memoryStore)

func WithClientConfig(client *oauth2.Config) MemoryStoreOption {
	return func(m *memoryStore) {
		issuer, _ := url.Base(client.Endpoint.AuthURL, http.SecureScheme)
		m.clients[issuer] = client
	}
}

type memoryStore struct {
	mu               sync.RWMutex
	issuerMetadata   map[string]*meta.AuthorizationServerMetadata
	issuerPublicKeys map[string]map[string]crypto.PublicKey
	clients          map[string]*oauth2.Config
	tokens           map[TokenKey]*oauth2.Token
}

func (m *memoryStore) LookupToken(key TokenKey) (*oauth2.Token, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.tokens != nil {
		if token, ok := m.tokens[key]; ok {
			return token, true
		}
	}
	return nil, false
}

func (m *memoryStore) AddToken(key TokenKey, token *oauth2.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens == nil {
		m.tokens = map[TokenKey]*oauth2.Token{}
	}
	m.tokens[key] = token
	return nil
}

func (m *memoryStore) LookupClientConfig(iss string) (*oauth2.Config, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.clients != nil {
		if id, ok := m.clients[iss]; ok {
			return id, true
		}
	}
	return nil, false
}
func (m *memoryStore) AddClientConfig(issuer string, client *oauth2.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[issuer] = client
	return nil
}

func (m *memoryStore) AddAuthorizationServerMetadata(metadata *meta.AuthorizationServerMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issuerMetadata[metadata.Issuer] = metadata
	return nil
}

func (m *memoryStore) LookupAuthorizationServerMetadata(issuer string) (*meta.AuthorizationServerMetadata, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.issuerMetadata != nil {
		if metadata, ok := m.issuerMetadata[issuer]; ok {
			return metadata, true
		}
	}
	return nil, false
}

func (m *memoryStore) AddIssuerPublicKeys(issuer string, keys map[string]crypto.PublicKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.issuerPublicKeys == nil {
		m.issuerPublicKeys = map[string]map[string]crypto.PublicKey{}
	}
	m.issuerPublicKeys[issuer] = keys
	return nil
}

func (m *memoryStore) LookupIssuerPublicKeys(issuer string) (map[string]crypto.PublicKey, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.issuerPublicKeys != nil {
		if keys, ok := m.issuerPublicKeys[issuer]; ok {
			return keys, true
		}
	}
	return nil, false
}

func NewMemoryStore(options ...MemoryStoreOption) Store {
	ret := &memoryStore{
		clients:          map[string]*oauth2.Config{},
		issuerMetadata:   map[string]*meta.AuthorizationServerMetadata{},
		issuerPublicKeys: map[string]map[string]crypto.PublicKey{},
	}
	for _, opt := range options {
		opt(ret)
	}
	return ret
}
